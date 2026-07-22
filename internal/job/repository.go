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

// ProgressRepository stores the latest durable progress independently of job
// ownership. Remote orchestrators may know progress for a worker-owned job
// without owning the worker's full durable job row.
type ProgressRepository interface {
	SetProgress(context.Context, ID, Progress) error
	GetProgress(context.Context, ID) (*Progress, error)
	ClearProgress(context.Context, ID) error
}
