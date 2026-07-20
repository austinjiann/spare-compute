package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

// Cursor returns the externally committed end of a job log.
func (repository *ExecutionRepository) Cursor(
	ctx context.Context,
	id job.ID,
) (joblogging.Cursor, error) {
	if !id.Valid() {
		return joblogging.Cursor{}, job.ErrInvalidID
	}
	var cursor joblogging.Cursor
	err := repository.database.QueryRowContext(ctx, `
		SELECT log_bytes, next_log_sequence
		FROM job_executions
		WHERE job_id = ?
	`, id).Scan(&cursor.DataOffset, &cursor.NextSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return joblogging.Cursor{}, fmt.Errorf("%w: %s", joblogging.ErrNotFound, id)
	}
	if err != nil {
		return joblogging.Cursor{}, fmt.Errorf("read job log cursor: %w", err)
	}
	return cursor, nil
}

// Commit atomically assigns the next sequence to bytes already synced to disk.
func (repository *ExecutionRepository) Commit(
	ctx context.Context,
	id job.ID,
	expectedOffset int64,
	stream joblogging.Stream,
	length int,
	at time.Time,
) (joblogging.Metadata, error) {
	metadata := joblogging.Metadata{
		JobID: id, Stream: stream, DataOffset: expectedOffset, DataLength: length, At: at.UTC(),
	}
	if !id.Valid() || expectedOffset < 0 || length <= 0 || length > joblogging.MaximumChunkBytes || at.IsZero() {
		return joblogging.Metadata{}, joblogging.ErrInvalidRecord
	}
	if stream != joblogging.StreamStdout && stream != joblogging.StreamStderr {
		return joblogging.Metadata{}, joblogging.ErrInvalidRecord
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return joblogging.Metadata{}, fmt.Errorf("begin log commit: %w", err)
	}
	defer transaction.Rollback()

	var (
		currentOffset int64
		nextSequence  uint64
		status        string
	)
	err = transaction.QueryRowContext(ctx, `
		SELECT log_bytes, next_log_sequence, status
		FROM job_executions
		WHERE job_id = ?
	`, id).Scan(&currentOffset, &nextSequence, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return joblogging.Metadata{}, fmt.Errorf("%w: %s", joblogging.ErrNotFound, id)
	}
	if err != nil {
		return joblogging.Metadata{}, fmt.Errorf("load log cursor for commit: %w", err)
	}
	if execution.Status(status) == execution.StatusCompleted {
		return joblogging.Metadata{}, execution.ErrAttemptCompleted
	}
	if currentOffset != expectedOffset {
		return joblogging.Metadata{}, fmt.Errorf(
			"%w: expected byte offset %d, current offset %d",
			joblogging.ErrConflict,
			expectedOffset,
			currentOffset,
		)
	}
	metadata.Sequence = nextSequence
	if err := metadata.Validate(); err != nil {
		return joblogging.Metadata{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO job_log_records (
			job_id, sequence, stream, data_offset, data_length, at_ns
		) VALUES (?, ?, ?, ?, ?, ?)
	`, id, nextSequence, stream, expectedOffset, length, at.UTC().UnixNano()); err != nil {
		return joblogging.Metadata{}, fmt.Errorf("insert job log record: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE job_executions
		SET log_bytes = ?, next_log_sequence = ?
		WHERE job_id = ? AND log_bytes = ? AND next_log_sequence = ?
	`, expectedOffset+int64(length), nextSequence+1, id, expectedOffset, nextSequence)
	if err != nil {
		return joblogging.Metadata{}, fmt.Errorf("advance job log cursor: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return joblogging.Metadata{}, fmt.Errorf("count advanced job log cursor: %w", err)
	}
	if updated != 1 {
		return joblogging.Metadata{}, joblogging.ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return joblogging.Metadata{}, fmt.Errorf("commit job log record: %w", err)
	}
	return metadata, nil
}

// List returns globally ordered metadata after an exclusive sequence offset.
func (repository *ExecutionRepository) List(
	ctx context.Context,
	id job.ID,
	after uint64,
	limit int,
) ([]joblogging.Metadata, bool, error) {
	if !id.Valid() {
		return nil, false, job.ErrInvalidID
	}
	if limit <= 0 || limit > joblogging.MaximumPageLimit {
		return nil, false, joblogging.ErrInvalidPage
	}
	var exists bool
	if err := repository.database.QueryRowContext(
		ctx,
		"SELECT EXISTS(SELECT 1 FROM job_executions WHERE job_id = ?)",
		id,
	).Scan(&exists); err != nil {
		return nil, false, fmt.Errorf("check job log existence: %w", err)
	}
	if !exists {
		return nil, false, fmt.Errorf("%w: %s", joblogging.ErrNotFound, id)
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT sequence, stream, data_offset, data_length, at_ns
		FROM job_log_records
		WHERE job_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, id, after, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list job log metadata: %w", err)
	}
	defer rows.Close()
	metadata := make([]joblogging.Metadata, 0, limit+1)
	for rows.Next() {
		item := joblogging.Metadata{JobID: id}
		var atNS int64
		if err := rows.Scan(
			&item.Sequence,
			&item.Stream,
			&item.DataOffset,
			&item.DataLength,
			&atNS,
		); err != nil {
			return nil, false, fmt.Errorf("scan job log metadata: %w", err)
		}
		item.At = time.Unix(0, atNS).UTC()
		if err := item.Validate(); err != nil {
			return nil, false, err
		}
		metadata = append(metadata, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate job log metadata: %w", err)
	}
	hasMore := len(metadata) > limit
	if hasMore {
		metadata = metadata[:limit]
	}
	return metadata, hasMore, nil
}
