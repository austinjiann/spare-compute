// Package mapper converts generated protocol messages at the domain boundary.
package mapper

import (
	"fmt"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/job"
)

var stateToProtocol = map[job.State]localv1.JobState{
	job.StateCreated:      localv1.JobState_JOB_STATE_CREATED,
	job.StateValidating:   localv1.JobState_JOB_STATE_VALIDATING,
	job.StateQueued:       localv1.JobState_JOB_STATE_QUEUED,
	job.StateSnapshotting: localv1.JobState_JOB_STATE_SNAPSHOTTING,
	job.StateTransferring: localv1.JobState_JOB_STATE_TRANSFERRING,
	job.StateStarting:     localv1.JobState_JOB_STATE_STARTING,
	job.StateRunning:      localv1.JobState_JOB_STATE_RUNNING,
	job.StateCollecting:   localv1.JobState_JOB_STATE_COLLECTING,
	job.StateRestoring:    localv1.JobState_JOB_STATE_RESTORING,
	job.StateSucceeded:    localv1.JobState_JOB_STATE_SUCCEEDED,
	job.StateFailed:       localv1.JobState_JOB_STATE_FAILED,
	job.StateCancelled:    localv1.JobState_JOB_STATE_CANCELLED,
	job.StateRejected:     localv1.JobState_JOB_STATE_REJECTED,
	job.StateLost:         localv1.JobState_JOB_STATE_LOST,
}

var protocolToState = func() map[localv1.JobState]job.State {
	result := make(map[localv1.JobState]job.State, len(stateToProtocol))
	for domain, protocol := range stateToProtocol {
		result[protocol] = domain
	}
	return result
}()

// SpecToProto converts a validated domain specification to its wire form.
func SpecToProto(spec job.Spec) (*localv1.JobSpec, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	executor, err := executorToProto(spec.Executor)
	if err != nil {
		return nil, err
	}
	return &localv1.JobSpec{
		Executable:       spec.Executable,
		Arguments:        append([]string(nil), spec.Arguments...),
		WorkingDirectory: spec.WorkingDirectory,
		Environment:      cloneMap(spec.Environment),
		Executor:         executor,
		ContainerImage:   spec.ContainerImage,
		Outputs:          append([]string(nil), spec.Outputs...),
	}, nil
}

// SpecFromProto validates and converts an untrusted wire specification.
func SpecFromProto(message *localv1.JobSpec) (job.Spec, error) {
	if message == nil {
		return job.Spec{}, fmt.Errorf("%w: job specification is required", job.ErrInvalidSpec)
	}
	executor, err := executorFromProto(message.GetExecutor())
	if err != nil {
		return job.Spec{}, err
	}
	spec := job.Spec{
		Executable:       message.GetExecutable(),
		Arguments:        append([]string(nil), message.GetArguments()...),
		WorkingDirectory: message.GetWorkingDirectory(),
		Environment:      cloneMap(message.GetEnvironment()),
		Executor:         executor,
		ContainerImage:   message.GetContainerImage(),
		Outputs:          append([]string(nil), message.GetOutputs()...),
	}
	if err := spec.Validate(); err != nil {
		return job.Spec{}, err
	}
	return spec, nil
}

// JobToProto converts a validated durable job to its wire form.
func JobToProto(value job.Job) (*localv1.Job, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	spec, err := SpecToProto(value.Spec)
	if err != nil {
		return nil, err
	}
	state, ok := stateToProtocol[value.State]
	if !ok {
		return nil, fmt.Errorf("%w: %q", job.ErrInvalidState, value.State)
	}
	message := &localv1.Job{
		Id:                string(value.ID),
		Spec:              spec,
		State:             state,
		CreatedAtUnixNano: value.CreatedAt.UTC().UnixNano(),
		UpdatedAtUnixNano: value.UpdatedAt.UTC().UnixNano(),
	}
	if value.Failure != nil {
		message.Failure = &localv1.Failure{
			Code:      value.Failure.Code,
			Message:   value.Failure.Message,
			Retryable: value.Failure.Retryable,
		}
	}
	return message, nil
}

// JobFromProto validates and converts an untrusted wire job.
func JobFromProto(message *localv1.Job) (job.Job, error) {
	if message == nil {
		return job.Job{}, fmt.Errorf("%w: job is required", job.ErrInvalidJob)
	}
	id, err := job.ParseID(message.GetId())
	if err != nil {
		return job.Job{}, err
	}
	spec, err := SpecFromProto(message.GetSpec())
	if err != nil {
		return job.Job{}, err
	}
	state, ok := protocolToState[message.GetState()]
	if !ok {
		return job.Job{}, fmt.Errorf("%w: protocol value %d", job.ErrInvalidState, message.GetState())
	}
	value := job.Job{
		ID:        id,
		Spec:      spec,
		State:     state,
		CreatedAt: time.Unix(0, message.GetCreatedAtUnixNano()).UTC(),
		UpdatedAt: time.Unix(0, message.GetUpdatedAtUnixNano()).UTC(),
	}
	if failure := message.GetFailure(); failure != nil {
		value.Failure = &job.Failure{
			Code:      failure.GetCode(),
			Message:   failure.GetMessage(),
			Retryable: failure.GetRetryable(),
		}
	}
	if err := value.Validate(); err != nil {
		return job.Job{}, err
	}
	return value, nil
}

// StatesFromProto converts and validates job-state filters.
func StatesFromProto(states []localv1.JobState) ([]job.State, error) {
	result := make([]job.State, len(states))
	for index, state := range states {
		converted, ok := protocolToState[state]
		if !ok {
			return nil, fmt.Errorf("%w: protocol value %d", job.ErrInvalidState, state)
		}
		result[index] = converted
	}
	return result, nil
}

func executorToProto(executor job.Executor) (localv1.Executor, error) {
	switch executor {
	case job.ExecutorNative:
		return localv1.Executor_EXECUTOR_NATIVE, nil
	case job.ExecutorContainer:
		return localv1.Executor_EXECUTOR_CONTAINER, nil
	default:
		return localv1.Executor_EXECUTOR_UNSPECIFIED, fmt.Errorf(
			"%w: unsupported executor %q",
			job.ErrInvalidSpec,
			executor,
		)
	}
}

func executorFromProto(executor localv1.Executor) (job.Executor, error) {
	switch executor {
	case localv1.Executor_EXECUTOR_NATIVE:
		return job.ExecutorNative, nil
	case localv1.Executor_EXECUTOR_CONTAINER:
		return job.ExecutorContainer, nil
	default:
		return "", fmt.Errorf("%w: unsupported executor value %d", job.ErrInvalidSpec, executor)
	}
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
