package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/transfer"
)

const (
	maximumRemoteListLimit  = 500
	maximumPreflightDigests = 8_192
)

var ErrInvalidStatus = errors.New("invalid worker status")

// RemoteJobController is the worker application boundary exposed to a paired orchestrator.
type RemoteJobController interface {
	Submit(context.Context, job.Spec) (job.Job, error)
	Get(context.Context, job.ID) (job.Job, error)
	List(context.Context, job.ListOptions) ([]job.Job, error)
	Cancel(context.Context, job.ID) (job.Job, error)
	ReadLogs(context.Context, job.ID, uint64, int) (JobLogs, error)
}

// RemoteJobIDController is implemented by workers that can accept a
// caller-provided durable job ID.
type RemoteJobIDController interface {
	SubmitWithID(context.Context, job.ID, job.Spec) (job.Job, error)
}

// RemoteSnapshotController is implemented by workers with project transfer enabled.
type RemoteSnapshotController interface {
	MissingChunks(context.Context, []snapshot.Digest) ([]snapshot.Digest, error)
	PutChunk(context.Context, snapshot.Digest, []byte) error
	SubmitSnapshot(context.Context, job.Spec, snapshot.Manifest, string) (job.Job, error)
}

// RemoteSnapshotIDController is implemented by workers that can materialize a
// snapshot under a caller-provided durable job ID.
type RemoteSnapshotIDController interface {
	SubmitSnapshotWithID(context.Context, job.ID, job.Spec, snapshot.Manifest, string) (job.Job, error)
}

type RemoteSnapshotReservationController interface {
	ReserveSnapshot(context.Context, snapshot.Digest, []snapshot.Digest) error
	ReleaseSnapshot(snapshot.Digest)
}

// RemoteArtifactController exposes only completed declared outputs and their referenced chunks.
type RemoteArtifactController interface {
	ReadArtifacts(context.Context, job.ID) (JobArtifacts, error)
	ReadArtifactChunk(context.Context, job.ID, snapshot.Digest) ([]byte, error)
	MarkArtifactsRetrieved(context.Context, job.ID) error
}

// RemoteHandler maps authenticated network requests to the durable worker job service.
type RemoteHandler struct {
	jobs   RemoteJobController
	status Status
}

// Status is authenticated, secret-free worker metadata exposed to paired
// orchestrators for scheduling and display.
type Status struct {
	Platform         string
	Architecture     string
	LogicalCPUCount  uint32
	TotalMemoryBytes uint64
	ToolIDs          []string
}

func (status Status) Validate() error {
	if status.Platform == "" && status.Architecture == "" &&
		status.LogicalCPUCount == 0 && status.TotalMemoryBytes == 0 {
		return ErrInvalidStatus
	}
	if validateStatusHint(status.Platform) != nil ||
		validateStatusHint(status.Architecture) != nil ||
		status.LogicalCPUCount > 4096 ||
		validateStatusToolIDs(status.ToolIDs) != nil {
		return ErrInvalidStatus
	}
	return nil
}

// RemoteHandlerOption configures optional authenticated worker operations.
type RemoteHandlerOption func(*RemoteHandler) error

func WithStatus(status Status) RemoteHandlerOption {
	return func(handler *RemoteHandler) error {
		if err := status.Validate(); err != nil {
			return err
		}
		handler.status = status
		return nil
	}
}

// NewRemoteHandler constructs the paired-worker protocol handler.
func NewRemoteHandler(jobs RemoteJobController, options ...RemoteHandlerOption) (*RemoteHandler, error) {
	if jobs == nil {
		return nil, ErrMissingDependency
	}
	handler := &RemoteHandler{jobs: jobs}
	for _, option := range options {
		if option == nil {
			return nil, ErrMissingDependency
		}
		if err := option(handler); err != nil {
			return nil, err
		}
	}
	return handler, nil
}

// Handle executes one request after the QUIC transport has authenticated a
// currently active orchestrator pin.
func (handler *RemoteHandler) Handle(
	ctx context.Context,
	request *computehopv1.RemoteRequest,
) *computehopv1.RemoteResponse {
	switch operation := request.GetOperation().(type) {
	case *computehopv1.RemoteRequest_SubmitJob:
		return handler.submit(ctx, operation.SubmitJob)
	case *computehopv1.RemoteRequest_GetJob:
		return handler.get(ctx, operation.GetJob)
	case *computehopv1.RemoteRequest_ListJobs:
		return handler.list(ctx, operation.ListJobs)
	case *computehopv1.RemoteRequest_CancelJob:
		return handler.cancel(ctx, operation.CancelJob)
	case *computehopv1.RemoteRequest_ReadJobLogs:
		return handler.readLogs(ctx, operation.ReadJobLogs)
	case *computehopv1.RemoteRequest_CheckSnapshot:
		return handler.checkSnapshot(ctx, operation.CheckSnapshot)
	case *computehopv1.RemoteRequest_PutChunk:
		return handler.putChunk(ctx, operation.PutChunk)
	case *computehopv1.RemoteRequest_GetJobArtifacts:
		return handler.getJobArtifacts(ctx, operation.GetJobArtifacts)
	case *computehopv1.RemoteRequest_GetArtifactChunk:
		return handler.getArtifactChunk(ctx, operation.GetArtifactChunk)
	case *computehopv1.RemoteRequest_AcknowledgeJobArtifacts:
		return handler.acknowledgeJobArtifacts(ctx, operation.AcknowledgeJobArtifacts)
	case *computehopv1.RemoteRequest_GetWorkerStatus:
		return handler.getWorkerStatus(operation.GetWorkerStatus)
	default:
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			"unsupported remote operation",
		)
	}
}

func (handler *RemoteHandler) getWorkerStatus(
	request *computehopv1.GetWorkerStatusRequest,
) *computehopv1.RemoteResponse {
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "worker status request is required")
	}
	if err := handler.status.Validate(); err != nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT, "worker status is unavailable")
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetWorkerStatus{
		GetWorkerStatus: &computehopv1.GetWorkerStatusResponse{
			Platform:         handler.status.Platform,
			Arch:             handler.status.Architecture,
			LogicalCpuCount:  handler.status.LogicalCPUCount,
			TotalMemoryBytes: handler.status.TotalMemoryBytes,
			ToolIds:          append([]string(nil), handler.status.ToolIDs...),
		},
	}}
}

func (handler *RemoteHandler) acknowledgeJobArtifacts(
	ctx context.Context,
	request *computehopv1.AcknowledgeJobArtifactsRequest,
) *computehopv1.RemoteResponse {
	controller, ok := handler.jobs.(RemoteArtifactController)
	if !ok {
		return remoteErrorResponse(ErrArtifactsDisabled)
	}
	if request == nil {
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			"artifact acknowledgment is required",
		)
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	if err := controller.MarkArtifactsRetrieved(ctx, id); err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_AcknowledgeJobArtifacts{
		AcknowledgeJobArtifacts: &computehopv1.AcknowledgeJobArtifactsResponse{JobId: string(id)},
	}}
}

func (handler *RemoteHandler) getJobArtifacts(
	ctx context.Context,
	request *computehopv1.GetJobArtifactsRequest,
) *computehopv1.RemoteResponse {
	controller, ok := handler.jobs.(RemoteArtifactController)
	if !ok {
		return remoteErrorResponse(ErrArtifactsDisabled)
	}
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "artifact request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	result, err := controller.ReadArtifacts(ctx, id)
	if err != nil {
		return remoteErrorResponse(err)
	}
	jobMessage, err := mapper.JobToRemoteProto(result.Job)
	if err != nil {
		return remoteErrorResponse(err)
	}
	manifest, err := mapper.ManifestToRemoteProto(result.Bundle.Manifest)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJobArtifacts{
		GetJobArtifacts: &computehopv1.GetJobArtifactsResponse{
			Job: jobMessage, Artifacts: manifest,
			CollectedAtUnixNano: result.Bundle.CollectedAt.UTC().UnixNano(),
		},
	}}
}

func (handler *RemoteHandler) getArtifactChunk(
	ctx context.Context,
	request *computehopv1.GetArtifactChunkRequest,
) *computehopv1.RemoteResponse {
	controller, ok := handler.jobs.(RemoteArtifactController)
	if !ok {
		return remoteErrorResponse(ErrArtifactsDisabled)
	}
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "artifact chunk request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	digest, err := snapshot.ParseDigest(request.GetDigest())
	if err != nil {
		return remoteErrorResponse(err)
	}
	accepted, err := mapper.ChunkEncodingsFromRemoteProto(request.GetAcceptedEncodings())
	if err != nil {
		return remoteErrorResponse(err)
	}
	contents, err := controller.ReadArtifactChunk(ctx, id, digest)
	if err != nil {
		return remoteErrorResponse(err)
	}
	if len(contents) == 0 || len(contents) > snapshot.MaximumChunkBytes || snapshot.Sum(contents) != digest {
		return remoteErrorResponse(snapshot.ErrInvalidDigest)
	}
	encoded, err := transfer.EncodeChunk(contents, accepted)
	if err != nil {
		return remoteErrorResponse(err)
	}
	encoding, err := mapper.ChunkEncodingToRemoteProto(encoded.Encoding)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetArtifactChunk{
		GetArtifactChunk: &computehopv1.GetArtifactChunkResponse{
			Digest: string(digest), Data: encoded.Data,
			Encoding: encoding, UncompressedSize: encoded.UncompressedSize,
		},
	}}
}

func (handler *RemoteHandler) submit(
	ctx context.Context,
	request *computehopv1.SubmitJobRequest,
) *computehopv1.RemoteResponse {
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "submit request is required")
	}
	spec, err := mapper.SpecFromRemoteProto(request.GetSpec())
	if err != nil {
		return remoteErrorResponse(err)
	}
	var requestedID job.ID
	if strings.TrimSpace(request.GetJobId()) != "" {
		requestedID, err = job.ParseID(request.GetJobId())
		if err != nil {
			return remoteErrorResponse(err)
		}
	}
	var value job.Job
	if request.GetSnapshot() == nil {
		if request.GetWorkingSubdirectory() != "" {
			return remoteFailure(
				computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
				"working subdirectory requires a project snapshot",
			)
		}
		if requestedID.Valid() {
			controller, ok := handler.jobs.(RemoteJobIDController)
			if !ok {
				return remoteFailure(
					computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNSUPPORTED_VERSION,
					"worker does not support caller-provided job IDs",
				)
			}
			value, err = controller.SubmitWithID(ctx, requestedID, spec)
		} else {
			value, err = handler.jobs.Submit(ctx, spec)
		}
	} else {
		controller, ok := handler.jobs.(RemoteSnapshotController)
		if !ok {
			return remoteErrorResponse(ErrSnapshotsDisabled)
		}
		manifest, manifestErr := mapper.ManifestFromRemoteProto(request.GetSnapshot())
		if manifestErr != nil {
			return remoteErrorResponse(manifestErr)
		}
		if reservations, ok := handler.jobs.(RemoteSnapshotReservationController); ok {
			manifestID, manifestErr := manifest.ID()
			if manifestErr != nil {
				return remoteErrorResponse(manifestErr)
			}
			defer reservations.ReleaseSnapshot(manifestID)
		}
		if requestedID.Valid() {
			idController, ok := handler.jobs.(RemoteSnapshotIDController)
			if !ok {
				return remoteFailure(
					computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNSUPPORTED_VERSION,
					"worker does not support caller-provided snapshot job IDs",
				)
			}
			value, err = idController.SubmitSnapshotWithID(ctx, requestedID, spec, manifest, request.GetWorkingSubdirectory())
		} else {
			value, err = controller.SubmitSnapshot(ctx, spec, manifest, request.GetWorkingSubdirectory())
		}
	}
	if err != nil {
		return remoteErrorResponse(err)
	}
	message, err := mapper.JobToRemoteProto(value)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
		SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
	}}
}

func (handler *RemoteHandler) checkSnapshot(
	ctx context.Context,
	request *computehopv1.CheckSnapshotRequest,
) *computehopv1.RemoteResponse {
	controller, ok := handler.jobs.(RemoteSnapshotController)
	if !ok {
		return remoteErrorResponse(ErrSnapshotsDisabled)
	}
	if request == nil || len(request.GetChunkDigests()) == 0 ||
		len(request.GetChunkDigests()) > maximumPreflightDigests {
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("snapshot preflight requires 1 to %d chunks", maximumPreflightDigests),
		)
	}
	manifestID, err := snapshot.ParseDigest(request.GetManifestId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	digests := make([]snapshot.Digest, len(request.GetChunkDigests()))
	for index, encoded := range request.GetChunkDigests() {
		digest, err := snapshot.ParseDigest(encoded)
		if err != nil {
			return remoteErrorResponse(err)
		}
		digests[index] = digest
	}
	if reservations, ok := handler.jobs.(RemoteSnapshotReservationController); ok {
		if err := reservations.ReserveSnapshot(ctx, manifestID, digests); err != nil {
			return remoteErrorResponse(err)
		}
	}
	missing, err := controller.MissingChunks(ctx, digests)
	if err != nil {
		return remoteErrorResponse(err)
	}
	encoded := make([]string, len(missing))
	for index, digest := range missing {
		encoded[index] = string(digest)
	}
	accepted, err := mapper.ChunkEncodingsToRemoteProto(transfer.SupportedChunkEncodings())
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_CheckSnapshot{
		CheckSnapshot: &computehopv1.CheckSnapshotResponse{
			MissingChunkDigests: encoded, AcceptedChunkEncodings: accepted,
		},
	}}
}

func (handler *RemoteHandler) putChunk(
	ctx context.Context,
	request *computehopv1.PutChunkRequest,
) *computehopv1.RemoteResponse {
	controller, ok := handler.jobs.(RemoteSnapshotController)
	if !ok {
		return remoteErrorResponse(ErrSnapshotsDisabled)
	}
	if request == nil || len(request.GetData()) == 0 || len(request.GetData()) > transfer.MaximumEncodedChunkBytes {
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("encoded snapshot chunk must contain 1 to %d bytes", transfer.MaximumEncodedChunkBytes),
		)
	}
	digest, err := snapshot.ParseDigest(request.GetDigest())
	if err != nil {
		return remoteErrorResponse(err)
	}
	encoding, err := mapper.ChunkEncodingFromRemoteProto(request.GetEncoding())
	if err != nil {
		return remoteErrorResponse(err)
	}
	contents, err := transfer.DecodeChunk(transfer.Chunk{
		Encoding: encoding, Data: request.GetData(), UncompressedSize: request.GetUncompressedSize(),
	})
	if err != nil {
		return remoteErrorResponse(err)
	}
	if snapshot.Sum(contents) != digest {
		return remoteErrorResponse(snapshot.ErrInvalidDigest)
	}
	if err := controller.PutChunk(ctx, digest, contents); err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_PutChunk{
		PutChunk: &computehopv1.PutChunkResponse{Digest: string(digest)},
	}}
}

func (handler *RemoteHandler) get(
	ctx context.Context,
	request *computehopv1.GetJobRequest,
) *computehopv1.RemoteResponse {
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "get request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	value, err := handler.jobs.Get(ctx, id)
	if err != nil {
		return remoteErrorResponse(err)
	}
	message, err := mapper.JobToRemoteProto(value)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
		GetJob: &computehopv1.GetJobResponse{Job: message},
	}}
}

func (handler *RemoteHandler) list(
	ctx context.Context,
	request *computehopv1.ListJobsRequest,
) *computehopv1.RemoteResponse {
	if request == nil || request.GetLimit() > maximumRemoteListLimit {
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job list limit cannot exceed %d", maximumRemoteListLimit),
		)
	}
	states, err := mapper.StatesFromRemoteProto(request.GetStates())
	if err != nil {
		return remoteErrorResponse(err)
	}
	values, err := handler.jobs.List(ctx, job.ListOptions{States: states, Limit: int(request.GetLimit())})
	if err != nil {
		return remoteErrorResponse(err)
	}
	messages := make([]*computehopv1.Job, len(values))
	for index, value := range values {
		messages[index], err = mapper.JobToRemoteProto(value)
		if err != nil {
			return remoteErrorResponse(err)
		}
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_ListJobs{
		ListJobs: &computehopv1.ListJobsResponse{Jobs: messages},
	}}
}

func (handler *RemoteHandler) cancel(
	ctx context.Context,
	request *computehopv1.CancelJobRequest,
) *computehopv1.RemoteResponse {
	if request == nil {
		return remoteFailure(computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT, "cancel request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	value, err := handler.jobs.Cancel(ctx, id)
	if err != nil {
		return remoteErrorResponse(err)
	}
	message, err := mapper.JobToRemoteProto(value)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_CancelJob{
		CancelJob: &computehopv1.CancelJobResponse{Job: message},
	}}
}

func (handler *RemoteHandler) readLogs(
	ctx context.Context,
	request *computehopv1.ReadJobLogsRequest,
) *computehopv1.RemoteResponse {
	if request == nil || request.GetLimit() > joblogging.MaximumPageLimit {
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job log limit cannot exceed %d", joblogging.MaximumPageLimit),
		)
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return remoteErrorResponse(err)
	}
	result, err := handler.jobs.ReadLogs(ctx, id, request.GetAfterSequence(), int(request.GetLimit()))
	if err != nil {
		return remoteErrorResponse(err)
	}
	jobMessage, err := mapper.JobToRemoteProto(result.Job)
	if err != nil {
		return remoteErrorResponse(err)
	}
	records, err := mapper.LogRecordsToRemoteProto(result.Page.Records)
	if err != nil {
		return remoteErrorResponse(err)
	}
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_ReadJobLogs{
		ReadJobLogs: &computehopv1.ReadJobLogsResponse{
			Job: jobMessage, Records: records, HasMore: result.Page.HasMore,
		},
	}}
}

func remoteErrorResponse(err error) *computehopv1.RemoteResponse {
	code := computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL
	message := "internal worker error"
	switch {
	case errors.Is(err, job.ErrInvalidID), errors.Is(err, job.ErrInvalidSpec),
		errors.Is(err, job.ErrInvalidJob), errors.Is(err, job.ErrInvalidState),
		errors.Is(err, job.ErrInvalidTransition), errors.Is(err, job.ErrInvalidProgress),
		errors.Is(err, joblogging.ErrInvalidPage),
		errors.Is(err, joblogging.ErrInvalidRecord), errors.Is(err, snapshot.ErrInvalidDigest),
		errors.Is(err, snapshot.ErrInvalidManifest), errors.Is(err, snapshot.ErrUnsafePath),
		errors.Is(err, transfer.ErrInvalidChunk), errors.Is(err, transfer.ErrUnsupportedEncoding):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT
		message = err.Error()
	case errors.Is(err, job.ErrNotFound), errors.Is(err, joblogging.ErrNotFound),
		errors.Is(err, execution.ErrNotFound), errors.Is(err, artifact.ErrNotFound):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_NOT_FOUND
		message = err.Error()
	case errors.Is(err, job.ErrConflict), errors.Is(err, execution.ErrNotClaimable),
		errors.Is(err, execution.ErrOwnerMismatch), errors.Is(err, execution.ErrAttemptCompleted):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, ErrJobTerminal):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_JOB_TERMINAL
		message = err.Error()
	case errors.Is(err, ErrSnapshotsDisabled), errors.Is(err, ErrSnapshotIncomplete):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, contentcache.ErrQuotaExceeded), errors.Is(err, contentcache.ErrReservationLimit):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, ErrArtifactsDisabled), errors.Is(err, ErrArtifactsNotReady):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT
		message = err.Error()
	}
	return remoteFailure(code, message)
}

func remoteFailure(code computehopv1.RemoteErrorCode, message string) *computehopv1.RemoteResponse {
	return &computehopv1.RemoteResponse{Error: &computehopv1.RemoteError{Code: code, Message: message}}
}

func validateStatusHint(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > 32 || !utf8.ValidString(value) {
		return ErrInvalidStatus
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '=' {
			return ErrInvalidStatus
		}
	}
	return nil
}

func validateStatusToolIDs(values []string) error {
	if len(values) > 128 || !slices.IsSorted(values) {
		return ErrInvalidStatus
	}
	previous := ""
	for _, value := range values {
		if value == "" || value == previous || validateStatusHint(value) != nil {
			return ErrInvalidStatus
		}
		previous = value
	}
	return nil
}
