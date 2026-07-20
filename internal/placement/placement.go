// Package placement models the orchestrator's durable record of which worker
// owns a remotely submitted job.
package placement

import (
	"context"
	"errors"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
)

var (
	ErrInvalid  = errors.New("invalid remote job placement")
	ErrNotFound = errors.New("remote job placement not found")
	ErrConflict = errors.New("remote job placement conflicts with existing placement")
)

// Placement remembers the pinned worker identity that accepted a job. The
// worker remains authoritative for the job itself.
type Placement struct {
	JobID    job.ID
	WorkerID device.ID
	PlacedAt time.Time
}

// Validate checks the durable routing fields.
func (value Placement) Validate() error {
	if !value.JobID.Valid() || !value.WorkerID.Valid() || value.PlacedAt.IsZero() {
		return ErrInvalid
	}
	return nil
}

// Repository is the persistence boundary for remote job routing metadata.
type Repository interface {
	Create(context.Context, Placement) error
	Get(context.Context, job.ID) (Placement, error)
}
