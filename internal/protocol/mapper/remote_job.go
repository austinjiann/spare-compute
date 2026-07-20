package mapper

import (
	"fmt"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

var stateToRemoteProtocol = map[job.State]computehopv1.JobState{
	job.StateCreated:      computehopv1.JobState_JOB_STATE_CREATED,
	job.StateValidating:   computehopv1.JobState_JOB_STATE_VALIDATING,
	job.StateQueued:       computehopv1.JobState_JOB_STATE_QUEUED,
	job.StateSnapshotting: computehopv1.JobState_JOB_STATE_SNAPSHOTTING,
	job.StateTransferring: computehopv1.JobState_JOB_STATE_TRANSFERRING,
	job.StateStarting:     computehopv1.JobState_JOB_STATE_STARTING,
	job.StateRunning:      computehopv1.JobState_JOB_STATE_RUNNING,
	job.StateCollecting:   computehopv1.JobState_JOB_STATE_COLLECTING,
	job.StateRestoring:    computehopv1.JobState_JOB_STATE_RESTORING,
	job.StateSucceeded:    computehopv1.JobState_JOB_STATE_SUCCEEDED,
	job.StateFailed:       computehopv1.JobState_JOB_STATE_FAILED,
	job.StateCancelled:    computehopv1.JobState_JOB_STATE_CANCELLED,
	job.StateRejected:     computehopv1.JobState_JOB_STATE_REJECTED,
	job.StateLost:         computehopv1.JobState_JOB_STATE_LOST,
}

var remoteProtocolToState = func() map[computehopv1.JobState]job.State {
	result := make(map[computehopv1.JobState]job.State, len(stateToRemoteProtocol))
	for domain, protocol := range stateToRemoteProtocol {
		result[protocol] = domain
	}
	return result
}()

// SpecToRemoteProto converts a validated specification to the paired-worker protocol.
func SpecToRemoteProto(spec job.Spec) (*computehopv1.JobSpec, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	executor, err := executorToRemoteProto(spec.Executor)
	if err != nil {
		return nil, err
	}
	return &computehopv1.JobSpec{
		Executable: spec.Executable, Arguments: append([]string(nil), spec.Arguments...),
		WorkingDirectory: spec.WorkingDirectory, Environment: cloneMap(spec.Environment),
		Executor: executor, ContainerImage: spec.ContainerImage,
	}, nil
}

// SpecFromRemoteProto validates an untrusted paired-worker specification.
func SpecFromRemoteProto(message *computehopv1.JobSpec) (job.Spec, error) {
	if message == nil {
		return job.Spec{}, fmt.Errorf("%w: job specification is required", job.ErrInvalidSpec)
	}
	executor, err := executorFromRemoteProto(message.GetExecutor())
	if err != nil {
		return job.Spec{}, err
	}
	spec := job.Spec{
		Executable: message.GetExecutable(), Arguments: append([]string(nil), message.GetArguments()...),
		WorkingDirectory: message.GetWorkingDirectory(), Environment: cloneMap(message.GetEnvironment()),
		Executor: executor, ContainerImage: message.GetContainerImage(),
	}
	if err := spec.Validate(); err != nil {
		return job.Spec{}, err
	}
	return spec, nil
}

// JobToRemoteProto converts a durable worker job to the remote wire protocol.
func JobToRemoteProto(value job.Job) (*computehopv1.Job, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	spec, err := SpecToRemoteProto(value.Spec)
	if err != nil {
		return nil, err
	}
	state, ok := stateToRemoteProtocol[value.State]
	if !ok {
		return nil, fmt.Errorf("%w: %q", job.ErrInvalidState, value.State)
	}
	message := &computehopv1.Job{
		Id: string(value.ID), Spec: spec, State: state,
		CreatedAtUnixNano: value.CreatedAt.UTC().UnixNano(), UpdatedAtUnixNano: value.UpdatedAt.UTC().UnixNano(),
	}
	if value.Failure != nil {
		message.Failure = &computehopv1.Failure{
			Code: value.Failure.Code, Message: value.Failure.Message, Retryable: value.Failure.Retryable,
		}
	}
	return message, nil
}

// JobFromRemoteProto validates a job returned by a paired worker.
func JobFromRemoteProto(message *computehopv1.Job) (job.Job, error) {
	if message == nil {
		return job.Job{}, fmt.Errorf("%w: job is required", job.ErrInvalidJob)
	}
	id, err := job.ParseID(message.GetId())
	if err != nil {
		return job.Job{}, err
	}
	spec, err := SpecFromRemoteProto(message.GetSpec())
	if err != nil {
		return job.Job{}, err
	}
	state, ok := remoteProtocolToState[message.GetState()]
	if !ok {
		return job.Job{}, fmt.Errorf("%w: protocol value %d", job.ErrInvalidState, message.GetState())
	}
	value := job.Job{
		ID: id, Spec: spec, State: state,
		CreatedAt: time.Unix(0, message.GetCreatedAtUnixNano()).UTC(),
		UpdatedAt: time.Unix(0, message.GetUpdatedAtUnixNano()).UTC(),
	}
	if failure := message.GetFailure(); failure != nil {
		value.Failure = &job.Failure{
			Code: failure.GetCode(), Message: failure.GetMessage(), Retryable: failure.GetRetryable(),
		}
	}
	if err := value.Validate(); err != nil {
		return job.Job{}, err
	}
	return value, nil
}

// StatesToRemoteProto maps validated domain state filters to the worker protocol.
func StatesToRemoteProto(states []job.State) ([]computehopv1.JobState, error) {
	result := make([]computehopv1.JobState, len(states))
	for index, state := range states {
		converted, ok := stateToRemoteProtocol[state]
		if !ok {
			return nil, fmt.Errorf("%w: %q", job.ErrInvalidState, state)
		}
		result[index] = converted
	}
	return result, nil
}

// StatesFromRemoteProto validates state filters received by a worker.
func StatesFromRemoteProto(states []computehopv1.JobState) ([]job.State, error) {
	result := make([]job.State, len(states))
	for index, state := range states {
		converted, ok := remoteProtocolToState[state]
		if !ok {
			return nil, fmt.Errorf("%w: protocol value %d", job.ErrInvalidState, state)
		}
		result[index] = converted
	}
	return result, nil
}

// LogRecordsToRemoteProto maps durable log records to the worker protocol.
func LogRecordsToRemoteProto(records []joblogging.Record) ([]*computehopv1.JobLogRecord, error) {
	messages := make([]*computehopv1.JobLogRecord, len(records))
	for index, record := range records {
		stream := computehopv1.JobLogStream_JOB_LOG_STREAM_UNSPECIFIED
		switch record.Stream {
		case joblogging.StreamStdout:
			stream = computehopv1.JobLogStream_JOB_LOG_STREAM_STDOUT
		case joblogging.StreamStderr:
			stream = computehopv1.JobLogStream_JOB_LOG_STREAM_STDERR
		default:
			return nil, fmt.Errorf("%w: unsupported stream %q", joblogging.ErrInvalidRecord, record.Stream)
		}
		if record.Sequence == 0 || len(record.Data) == 0 || len(record.Data) > joblogging.MaximumChunkBytes ||
			record.At.IsZero() {
			return nil, joblogging.ErrInvalidRecord
		}
		messages[index] = &computehopv1.JobLogRecord{
			Sequence: record.Sequence, Stream: stream, Data: append([]byte(nil), record.Data...),
			AtUnixNano: record.At.UTC().UnixNano(),
		}
	}
	return messages, nil
}

// LogRecordsFromRemoteProto validates records returned by a paired worker.
func LogRecordsFromRemoteProto(messages []*computehopv1.JobLogRecord) ([]joblogging.Record, error) {
	records := make([]joblogging.Record, len(messages))
	var previous uint64
	for index, message := range messages {
		if message == nil || message.GetSequence() == 0 || message.GetSequence() <= previous ||
			len(message.GetData()) == 0 || len(message.GetData()) > joblogging.MaximumChunkBytes ||
			message.GetAtUnixNano() == 0 {
			return nil, joblogging.ErrInvalidRecord
		}
		stream := joblogging.Stream("")
		switch message.GetStream() {
		case computehopv1.JobLogStream_JOB_LOG_STREAM_STDOUT:
			stream = joblogging.StreamStdout
		case computehopv1.JobLogStream_JOB_LOG_STREAM_STDERR:
			stream = joblogging.StreamStderr
		default:
			return nil, joblogging.ErrInvalidRecord
		}
		records[index] = joblogging.Record{
			Sequence: message.GetSequence(), Stream: stream, Data: append([]byte(nil), message.GetData()...),
			At: time.Unix(0, message.GetAtUnixNano()).UTC(),
		}
		previous = message.GetSequence()
	}
	return records, nil
}

func executorToRemoteProto(executor job.Executor) (computehopv1.Executor, error) {
	switch executor {
	case job.ExecutorNative:
		return computehopv1.Executor_EXECUTOR_NATIVE, nil
	case job.ExecutorContainer:
		return computehopv1.Executor_EXECUTOR_CONTAINER, nil
	default:
		return computehopv1.Executor_EXECUTOR_UNSPECIFIED, fmt.Errorf(
			"%w: unsupported executor %q", job.ErrInvalidSpec, executor,
		)
	}
}

func executorFromRemoteProto(executor computehopv1.Executor) (job.Executor, error) {
	switch executor {
	case computehopv1.Executor_EXECUTOR_NATIVE:
		return job.ExecutorNative, nil
	case computehopv1.Executor_EXECUTOR_CONTAINER:
		return job.ExecutorContainer, nil
	default:
		return "", fmt.Errorf("%w: unsupported executor value %d", job.ErrInvalidSpec, executor)
	}
}
