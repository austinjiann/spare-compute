package worker

import (
	"context"
	"errors"
	"fmt"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
)

const maximumRemoteListLimit = 500

// RemoteJobController is the worker application boundary exposed to a paired orchestrator.
type RemoteJobController interface {
	Submit(context.Context, job.Spec) (job.Job, error)
	Get(context.Context, job.ID) (job.Job, error)
	List(context.Context, job.ListOptions) ([]job.Job, error)
	Cancel(context.Context, job.ID) (job.Job, error)
	ReadLogs(context.Context, job.ID, uint64, int) (JobLogs, error)
}

// RemoteHandler maps authenticated network requests to the durable worker job service.
type RemoteHandler struct {
	jobs RemoteJobController
}

// NewRemoteHandler constructs the paired-worker protocol handler.
func NewRemoteHandler(jobs RemoteJobController) (*RemoteHandler, error) {
	if jobs == nil {
		return nil, ErrMissingDependency
	}
	return &RemoteHandler{jobs: jobs}, nil
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
	default:
		return remoteFailure(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			"unsupported remote operation",
		)
	}
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
	value, err := handler.jobs.Submit(ctx, spec)
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
		errors.Is(err, job.ErrInvalidTransition), errors.Is(err, joblogging.ErrInvalidPage),
		errors.Is(err, joblogging.ErrInvalidRecord):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT
		message = err.Error()
	case errors.Is(err, job.ErrNotFound), errors.Is(err, joblogging.ErrNotFound),
		errors.Is(err, execution.ErrNotFound):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_NOT_FOUND
		message = err.Error()
	case errors.Is(err, job.ErrConflict), errors.Is(err, execution.ErrNotClaimable),
		errors.Is(err, execution.ErrOwnerMismatch), errors.Is(err, execution.ErrAttemptCompleted):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, ErrJobTerminal):
		code = computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_JOB_TERMINAL
		message = err.Error()
	}
	return remoteFailure(code, message)
}

func remoteFailure(code computehopv1.RemoteErrorCode, message string) *computehopv1.RemoteResponse {
	return &computehopv1.RemoteResponse{Error: &computehopv1.RemoteError{Code: code, Message: message}}
}
