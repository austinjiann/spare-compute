// Package worker coordinates job use cases owned by a ComputeHop worker.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

var (
	ErrMissingDependency = errors.New("worker job service dependency is required")
	ErrJobTerminal       = errors.New("job is already terminal")
)

// Dependencies are explicit so production wiring and tests use the same
// constructor without global clocks or random generators.
type Dependencies struct {
	Jobs       job.Repository
	GenerateID func() (job.ID, error)
	Now        func() time.Time
}

// JobService coordinates durable worker-side job state.
type JobService struct {
	jobs       job.Repository
	generateID func() (job.ID, error)
	now        func() time.Time
}

// NewJobService validates and constructs the worker job application service.
func NewJobService(dependencies Dependencies) (*JobService, error) {
	if dependencies.Jobs == nil {
		return nil, fmt.Errorf("%w: Jobs", ErrMissingDependency)
	}
	if dependencies.GenerateID == nil {
		return nil, fmt.Errorf("%w: GenerateID", ErrMissingDependency)
	}
	if dependencies.Now == nil {
		return nil, fmt.Errorf("%w: Now", ErrMissingDependency)
	}
	return &JobService{
		jobs:       dependencies.Jobs,
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

// Cancel records an acknowledged cancellation. Repeating cancellation after a
// job is cancelled is idempotent and returns the existing terminal state.
func (service *JobService) Cancel(ctx context.Context, id job.ID) (job.Job, error) {
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
	return service.advanceLoaded(ctx, current, job.StateCancelled, nil)
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
