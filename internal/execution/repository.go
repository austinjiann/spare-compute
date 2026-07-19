package execution

import (
	"context"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

// Repository atomically coordinates job state with runner-owned execution data.
type Repository interface {
	Claim(context.Context, job.ID, int, time.Time) (Attempt, error)
	MarkRunning(context.Context, job.ID, int, int, time.Time) (Attempt, error)
	Heartbeat(context.Context, job.ID, int, time.Time) error
	RequestCancellation(context.Context, job.ID, time.Time) error
	CancellationRequested(context.Context, job.ID, int) (bool, error)
	Complete(context.Context, job.ID, int, Completion) (job.Job, Attempt, error)
	Get(context.Context, job.ID) (Attempt, error)
	ListActive(context.Context) ([]Attempt, error)
}
