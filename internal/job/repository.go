package job

import (
	"context"
	"errors"
)

var (
	ErrNotFound = errors.New("job not found")
	ErrConflict = errors.New("job changed concurrently")
)

// ListOptions bounds and filters job-history queries.
type ListOptions struct {
	States []State
	Limit  int
}

// Repository is the persistence boundary required by job workflows.
//
// ApplyTransition implementations must atomically verify transition.From,
// update the current job, and append the transition record. They return
// ErrConflict when another writer already changed the job.
type Repository interface {
	Create(context.Context, Job) error
	Get(context.Context, ID) (Job, error)
	List(context.Context, ListOptions) ([]Job, error)
	ApplyTransition(context.Context, Transition) (Job, error)
}
