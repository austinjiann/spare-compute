// Package orchestrator coordinates control-plane use cases owned by the Mac daemon.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
)

const maximumLocalListLimit = 500

var (
	ErrMissingJobController    = errors.New("local handler job controller is required")
	ErrMissingDeviceController = errors.New("local handler device controller is required")
)

// JobController is the narrow application boundary exposed over local IPC.
type JobController interface {
	Submit(context.Context, job.Spec) (job.Job, error)
	Get(context.Context, job.ID) (job.Job, error)
	List(context.Context, job.ListOptions) ([]job.Job, error)
	Cancel(context.Context, job.ID) (job.Job, error)
	ReadLogs(context.Context, job.ID, uint64, int) (worker.JobLogs, error)
}

// DeviceController is the narrow nearby-device boundary exposed over local IPC.
type DeviceController interface {
	ListNearby(context.Context) (device.DiscoverySnapshot, error)
}

// LocalHandler maps authenticated protocol requests to application use cases.
type LocalHandler struct {
	jobs    JobController
	devices DeviceController
	version string
}

// NewLocalHandler constructs the local orchestrator control handler.
func NewLocalHandler(jobs JobController, devices DeviceController, version string) (*LocalHandler, error) {
	if jobs == nil {
		return nil, ErrMissingJobController
	}
	if devices == nil {
		return nil, ErrMissingDeviceController
	}
	return &LocalHandler{jobs: jobs, devices: devices, version: version}, nil
}

// Handle executes one already-authenticated local request.
func (handler *LocalHandler) Handle(ctx context.Context, request *localv1.Request) *localv1.Response {
	switch operation := request.GetOperation().(type) {
	case *localv1.Request_Ping:
		return &localv1.Response{Result: &localv1.Response_Ping{Ping: &localv1.PingResponse{
			DaemonVersion: handler.version,
		}}}
	case *localv1.Request_SubmitJob:
		return handler.submit(ctx, operation.SubmitJob)
	case *localv1.Request_GetJob:
		return handler.get(ctx, operation.GetJob)
	case *localv1.Request_ListJobs:
		return handler.list(ctx, operation.ListJobs)
	case *localv1.Request_CancelJob:
		return handler.cancel(ctx, operation.CancelJob)
	case *localv1.Request_ReadJobLogs:
		return handler.readLogs(ctx, operation.ReadJobLogs)
	case *localv1.Request_ListDevices:
		return handler.listDevices(ctx, operation.ListDevices)
	default:
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "unsupported local operation")
	}
}

func (handler *LocalHandler) listDevices(
	ctx context.Context,
	request *localv1.ListDevicesRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "device list request is required")
	}
	snapshot, err := handler.devices.ListNearby(ctx)
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.DiscoverySnapshotToProto(snapshot)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ListDevices{ListDevices: message}}
}

func (handler *LocalHandler) readLogs(
	ctx context.Context,
	request *localv1.ReadJobLogsRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "log request is required")
	}
	if request.GetLimit() > joblogging.MaximumPageLimit {
		return failureResponse(
			localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job log limit cannot exceed %d", joblogging.MaximumPageLimit),
		)
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	result, err := handler.jobs.ReadLogs(ctx, id, request.GetAfterSequence(), int(request.GetLimit()))
	if err != nil {
		return errorResponse(err)
	}
	jobMessage, err := mapper.JobToProto(result.Job)
	if err != nil {
		return errorResponse(err)
	}
	records, err := mapper.LogRecordsToProto(result.Page.Records)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ReadJobLogs{ReadJobLogs: &localv1.ReadJobLogsResponse{
		Job:     jobMessage,
		Records: records,
		HasMore: result.Page.HasMore,
	}}}
}

func (handler *LocalHandler) submit(
	ctx context.Context,
	request *localv1.SubmitJobRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "submit request is required")
	}
	spec, err := mapper.SpecFromProto(request.GetSpec())
	if err != nil {
		return errorResponse(err)
	}
	value, err := handler.jobs.Submit(ctx, spec)
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_SubmitJob{SubmitJob: &localv1.SubmitJobResponse{
		Job: message,
	}}}
}

func (handler *LocalHandler) get(ctx context.Context, request *localv1.GetJobRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "get request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	value, err := handler.jobs.Get(ctx, id)
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_GetJob{GetJob: &localv1.GetJobResponse{
		Job: message,
	}}}
}

func (handler *LocalHandler) list(ctx context.Context, request *localv1.ListJobsRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "list request is required")
	}
	if request.GetLimit() > maximumLocalListLimit {
		return failureResponse(
			localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job list limit cannot exceed %d", maximumLocalListLimit),
		)
	}
	states, err := mapper.StatesFromProto(request.GetStates())
	if err != nil {
		return errorResponse(err)
	}
	values, err := handler.jobs.List(ctx, job.ListOptions{States: states, Limit: int(request.GetLimit())})
	if err != nil {
		return errorResponse(err)
	}
	messages := make([]*localv1.Job, len(values))
	for index, value := range values {
		messages[index], err = mapper.JobToProto(value)
		if err != nil {
			return errorResponse(err)
		}
	}
	return &localv1.Response{Result: &localv1.Response_ListJobs{ListJobs: &localv1.ListJobsResponse{
		Jobs: messages,
	}}}
}

func (handler *LocalHandler) cancel(ctx context.Context, request *localv1.CancelJobRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "cancel request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	value, err := handler.jobs.Cancel(ctx, id)
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_CancelJob{CancelJob: &localv1.CancelJobResponse{
		Job: message,
	}}}
}

func errorResponse(err error) *localv1.Response {
	code := localv1.ErrorCode_ERROR_CODE_INTERNAL
	message := "internal daemon error"
	switch {
	case errors.Is(err, job.ErrInvalidID),
		errors.Is(err, job.ErrInvalidSpec),
		errors.Is(err, job.ErrInvalidJob),
		errors.Is(err, job.ErrInvalidState),
		errors.Is(err, job.ErrInvalidTransition),
		errors.Is(err, joblogging.ErrInvalidPage),
		errors.Is(err, joblogging.ErrInvalidRecord):
		code = localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT
		message = err.Error()
	case errors.Is(err, job.ErrNotFound), errors.Is(err, joblogging.ErrNotFound):
		code = localv1.ErrorCode_ERROR_CODE_NOT_FOUND
		message = err.Error()
	case errors.Is(err, job.ErrConflict):
		code = localv1.ErrorCode_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, worker.ErrJobTerminal):
		code = localv1.ErrorCode_ERROR_CODE_JOB_TERMINAL
		message = err.Error()
	}
	return failureResponse(code, message)
}

func failureResponse(code localv1.ErrorCode, message string) *localv1.Response {
	return &localv1.Response{Error: &localv1.Error{Code: code, Message: message}}
}
