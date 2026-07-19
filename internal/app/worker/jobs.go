// Package worker coordinates job use cases owned by a ComputeHop worker.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

var (
	ErrMissingDependency = errors.New("worker job service dependency is required")
	ErrJobTerminal       = errors.New("job is already terminal")
)

// Dependencies are explicit so production wiring and tests use the same
// constructor without global clocks or random generators.
type Dependencies struct {
	Jobs       job.Repository
	Executions ExecutionController
	Logs       LogReader
	GenerateID func() (job.ID, error)
	Now        func() time.Time
}

// ExecutionController is the execution state required by local job commands.
type ExecutionController interface {
	RequestCancellation(context.Context, job.ID, time.Time) error
	Get(context.Context, job.ID) (execution.Attempt, error)
}

// LogReader pages durable, globally sequenced process output.
type LogReader interface {
	Read(context.Context, job.ID, uint64, int) (joblogging.Page, error)
}

// JobLogs is one authoritative snapshot used by the local logs command.
type JobLogs struct {
	Job       job.Job
	Execution *execution.Attempt
	Page      joblogging.Page
}

// JobService coordinates durable worker-side job state.
type JobService struct {
	jobs       job.Repository
	executions ExecutionController
	logs       LogReader
	generateID func() (job.ID, error)
	now        func() time.Time
}

// NewJobService validates and constructs the worker job application service.
func NewJobService(dependencies Dependencies) (*JobService, error) {
	if dependencies.Jobs == nil {
		return nil, fmt.Errorf("%w: Jobs", ErrMissingDependency)
	}
	if dependencies.Executions == nil {
		return nil, fmt.Errorf("%w: Executions", ErrMissingDependency)
	}
	if dependencies.Logs == nil {
		return nil, fmt.Errorf("%w: Logs", ErrMissingDependency)
	}
	if dependencies.GenerateID == nil {
		return nil, fmt.Errorf("%w: GenerateID", ErrMissingDependency)
	}
	if dependencies.Now == nil {
		return nil, fmt.Errorf("%w: Now", ErrMissingDependency)
	}
	return &JobService{
		jobs:       dependencies.Jobs,
		executions: dependencies.Executions,
		logs:       dependencies.Logs,
		generateID: dependencies.GenerateID,
		now:        dependencies.Now,
	}, nil
}

// Submit validates and durably accepts a job into the worker queue.
func (service *JobService) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	id, err := service.generateID()
	if err != nil {
		return job.Job{}, fmt.Errorf("generate job ID: %w", err)
	}
	created, err := job.New(id, spec, service.now())
	if err != nil {
		return job.Job{}, err
	}
	if err := service.jobs.Create(ctx, created); err != nil {
		return job.Job{}, fmt.Errorf("persist submitted job: %w", err)
	}

	validating, err := service.advanceLoaded(ctx, created, job.StateValidating, nil)
	if err != nil {
		return job.Job{}, fmt.Errorf("begin job validation: %w", err)
	}
	queued, err := service.advanceLoaded(ctx, validating, job.StateQueued, nil)
	if err != nil {
		return job.Job{}, fmt.Errorf("queue validated job: %w", err)
	}
	return queued, nil
}

// Get returns the worker's authoritative durable view of a job.
func (service *JobService) Get(ctx context.Context, id job.ID) (job.Job, error) {
	return service.jobs.Get(ctx, id)
}

// List returns durable worker jobs matching options.
func (service *JobService) List(ctx context.Context, options job.ListOptions) ([]job.Job, error) {
	return service.jobs.List(ctx, options)
}

// ReadLogs returns a job, its optional attempt, and one resumable output page.
func (service *JobService) ReadLogs(
	ctx context.Context,
	id job.ID,
	after uint64,
	limit int,
) (JobLogs, error) {
	current, err := service.jobs.Get(ctx, id)
	if err != nil {
		return JobLogs{}, err
	}
	result := JobLogs{Job: current}
	attempt, err := service.executions.Get(ctx, id)
	if err == nil {
		result.Execution = &attempt
	} else if !errors.Is(err, execution.ErrNotFound) {
		return JobLogs{}, err
	}
	if result.Execution == nil {
		return result, nil
	}
	result.Page, err = service.logs.Read(ctx, id, after, limit)
	if err != nil {
		return JobLogs{}, err
	}
	return result, nil
}

// Advance performs one validated, atomic state transition.
func (service *JobService) Advance(
	ctx context.Context,
	id job.ID,
	to job.State,
	failure *job.Failure,
) (job.Job, error) {
	current, err := service.jobs.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}
	return service.advanceLoaded(ctx, current, to, failure)
}

// Cancel either cancels a job that has not started or durably asks its owning
// runner to stop. Running jobs become cancelled only after the process tree exits.
func (service *JobService) Cancel(ctx context.Context, id job.ID) (job.Job, error) {
	for attempts := 0; attempts < 2; attempts++ {
		current, err := service.jobs.Get(ctx, id)
		if err != nil {
			return job.Job{}, err
		}
		if current.State == job.StateCancelled {
			return current, nil
		}
		if current.State.Terminal() {
			return job.Job{}, fmt.Errorf("%w: %s is %s", ErrJobTerminal, id, current.State)
		}
		if current.State == job.StateStarting || current.State == job.StateRunning {
			if err := service.executions.RequestCancellation(ctx, id, service.now()); err != nil {
				if errors.Is(err, execution.ErrAttemptCompleted) {
					return service.jobs.Get(ctx, id)
				}
				return job.Job{}, err
			}
			return service.jobs.Get(ctx, id)
		}
		cancelled, err := service.advanceLoaded(ctx, current, job.StateCancelled, nil)
		if !errors.Is(err, job.ErrConflict) {
			return cancelled, err
		}
	}
	return job.Job{}, job.ErrConflict
}

func (service *JobService) advanceLoaded(
	ctx context.Context,
	current job.Job,
	to job.State,
	failure *job.Failure,
) (job.Job, error) {
	_, transition, err := current.Apply(to, service.now(), failure)
	if err != nil {
		return job.Job{}, err
	}
	updated, err := service.jobs.ApplyTransition(ctx, transition)
	if err != nil {
		return job.Job{}, err
	}
	return updated, nil
}
