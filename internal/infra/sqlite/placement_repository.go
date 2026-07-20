package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/placement"
)

// PlacementRepository persists the orchestrator's remote job routing metadata.
type PlacementRepository struct {
	database *sql.DB
}

var _ placement.Repository = (*PlacementRepository)(nil)

// Create records the worker that accepted a remote job. Repeating the same
// job-to-worker mapping is idempotent; changing its worker is a conflict.
func (repository *PlacementRepository) Create(ctx context.Context, value placement.Placement) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO remote_job_placements (job_id, worker_device_id, placed_at_ns)
		VALUES (?, ?, ?)
	`, value.JobID, value.WorkerID, value.PlacedAt.UTC().UnixNano())
	if err == nil {
		return nil
	}
	if !isConstraintError(err) {
		return fmt.Errorf("create remote job placement %s: %w", value.JobID, err)
	}

	existing, loadErr := repository.Get(ctx, value.JobID)
	if loadErr == nil && existing.WorkerID == value.WorkerID {
		return nil
	}
	if loadErr != nil && !errors.Is(loadErr, placement.ErrNotFound) {
		return loadErr
	}
	return fmt.Errorf("%w: job %s", placement.ErrConflict, value.JobID)
}

// Get loads the durable worker target for a remote job.
func (repository *PlacementRepository) Get(ctx context.Context, id job.ID) (placement.Placement, error) {
	if !id.Valid() {
		return placement.Placement{}, job.ErrInvalidID
	}
	var workerID string
	var placedAtNS int64
	err := repository.database.QueryRowContext(ctx, `
		SELECT worker_device_id, placed_at_ns
		FROM remote_job_placements
		WHERE job_id = ?
	`, id).Scan(&workerID, &placedAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return placement.Placement{}, fmt.Errorf("%w: %s", placement.ErrNotFound, id)
	}
	if err != nil {
		return placement.Placement{}, fmt.Errorf("get remote job placement %s: %w", id, err)
	}
	parsedWorkerID, err := device.ParseID(workerID)
	if err != nil {
		return placement.Placement{}, fmt.Errorf("decode remote job placement %s: %w", id, err)
	}
	value := placement.Placement{
		JobID: id, WorkerID: parsedWorkerID, PlacedAt: time.Unix(0, placedAtNS).UTC(),
	}
	if err := value.Validate(); err != nil {
		return placement.Placement{}, fmt.Errorf("decode remote job placement %s: %w", id, err)
	}
	return value, nil
}
