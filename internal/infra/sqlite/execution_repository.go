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

const executionColumns = `
	job_id,
	status,
	runner_pid,
	process_pid,
	claimed_at_ns,
	started_at_ns,
	heartbeat_at_ns,
	cancel_requested_at_ns,
	finished_at_ns,
	exit_code,
	termination_signal,
	completion
`

// ExecutionRepository coordinates runner custody and external job-log indexes.
type ExecutionRepository struct {
	database *sql.DB
}

var (
	_ execution.Repository  = (*ExecutionRepository)(nil)
	_ joblogging.Repository = (*ExecutionRepository)(nil)
)

// Claim atomically moves a queued job to starting and assigns one runner.
func (repository *ExecutionRepository) Claim(
	ctx context.Context,
	id job.ID,
	runnerPID int,
	at time.Time,
) (execution.Attempt, error) {
	if !id.Valid() {
		return execution.Attempt{}, job.ErrInvalidID
	}
	if runnerPID <= 0 || at.IsZero() {
		return execution.Attempt{}, execution.ErrInvalidAttempt
	}
	at = at.UTC()
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("begin execution claim: %w", err)
	}
	defer transaction.Rollback()

	current, err := queryJob(ctx, transaction, `SELECT `+jobColumns+` FROM jobs
		LEFT JOIN job_progress ON job_progress.job_id = jobs.id
		WHERE jobs.id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Attempt{}, fmt.Errorf("%w: %s", job.ErrNotFound, id)
	}
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("load job for execution claim: %w", err)
	}
	if current.State != job.StateQueued {
		return execution.Attempt{}, fmt.Errorf("%w: job %s is %s", execution.ErrNotClaimable, id, current.State)
	}
	transition := job.Transition{JobID: id, From: job.StateQueued, To: job.StateStarting, At: at}
	if _, err := applyJobTransition(ctx, transaction, transition); err != nil {
		return execution.Attempt{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO job_executions (
			job_id, status, runner_pid, claimed_at_ns, heartbeat_at_ns
		) VALUES (?, ?, ?, ?, ?)
	`, id, execution.StatusStarting, runnerPID, at.UnixNano(), at.UnixNano()); err != nil {
		if isConstraintError(err) {
			return execution.Attempt{}, fmt.Errorf("%w: job %s already has an attempt", execution.ErrNotClaimable, id)
		}
		return execution.Attempt{}, fmt.Errorf("create execution attempt: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.Attempt{}, fmt.Errorf("commit execution claim: %w", err)
	}
	attempt := execution.Attempt{
		JobID:       id,
		Status:      execution.StatusStarting,
		RunnerPID:   runnerPID,
		ClaimedAt:   at,
		HeartbeatAt: at,
	}
	return attempt, attempt.Validate()
}

// MarkRunning atomically records the child PID and moves the job to running.
func (repository *ExecutionRepository) MarkRunning(
	ctx context.Context,
	id job.ID,
	runnerPID int,
	processPID int,
	at time.Time,
) (execution.Attempt, error) {
	if !id.Valid() {
		return execution.Attempt{}, job.ErrInvalidID
	}
	if runnerPID <= 0 || processPID <= 0 || at.IsZero() {
		return execution.Attempt{}, execution.ErrInvalidAttempt
	}
	at = at.UTC()
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("begin running execution: %w", err)
	}
	defer transaction.Rollback()

	attempt, err := getAttempt(ctx, transaction, id)
	if err != nil {
		return execution.Attempt{}, err
	}
	if err := requireActiveOwner(attempt, runnerPID, execution.StatusStarting); err != nil {
		return execution.Attempt{}, err
	}
	if at.Before(attempt.ClaimedAt) {
		return execution.Attempt{}, execution.ErrInvalidAttempt
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE job_executions
		SET status = ?, process_pid = ?, started_at_ns = ?, heartbeat_at_ns = ?
		WHERE job_id = ? AND status = ? AND runner_pid = ?
	`, execution.StatusRunning, processPID, at.UnixNano(), at.UnixNano(), id, execution.StatusStarting, runnerPID)
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("mark execution running: %w", err)
	}
	if err := requireOneAffected(result, "mark execution running"); err != nil {
		return execution.Attempt{}, err
	}
	transition := job.Transition{JobID: id, From: job.StateStarting, To: job.StateRunning, At: at}
	if _, err := applyJobTransition(ctx, transaction, transition); err != nil {
		return execution.Attempt{}, err
	}
	if err := transaction.Commit(); err != nil {
		return execution.Attempt{}, fmt.Errorf("commit running execution: %w", err)
	}
	attempt.Status = execution.StatusRunning
	attempt.ProcessPID = processPID
	attempt.StartedAt = timePointer(at)
	attempt.HeartbeatAt = at
	return attempt, attempt.Validate()
}

// Heartbeat proves that the runner retaining custody is alive.
func (repository *ExecutionRepository) Heartbeat(
	ctx context.Context,
	id job.ID,
	runnerPID int,
	at time.Time,
) error {
	if !id.Valid() {
		return job.ErrInvalidID
	}
	if runnerPID <= 0 || at.IsZero() {
		return execution.ErrInvalidAttempt
	}
	result, err := repository.database.ExecContext(ctx, `
		UPDATE job_executions
		SET heartbeat_at_ns = ?
		WHERE job_id = ? AND runner_pid = ? AND status IN (?, ?)
	`, at.UTC().UnixNano(), id, runnerPID, execution.StatusStarting, execution.StatusRunning)
	if err != nil {
		return fmt.Errorf("record execution heartbeat: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count execution heartbeat: %w", err)
	}
	if updated == 1 {
		return nil
	}
	return repository.explainOwnershipFailure(ctx, id, runnerPID)
}

// RequestCancellation durably asks the runner to stop its complete process tree.
func (repository *ExecutionRepository) RequestCancellation(
	ctx context.Context,
	id job.ID,
	at time.Time,
) error {
	if !id.Valid() {
		return job.ErrInvalidID
	}
	if at.IsZero() {
		return execution.ErrInvalidAttempt
	}
	result, err := repository.database.ExecContext(ctx, `
		UPDATE job_executions
		SET cancel_requested_at_ns = COALESCE(cancel_requested_at_ns, ?)
		WHERE job_id = ? AND status IN (?, ?)
	`, at.UTC().UnixNano(), id, execution.StatusStarting, execution.StatusRunning)
	if err != nil {
		return fmt.Errorf("request execution cancellation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count cancellation request: %w", err)
	}
	if updated == 1 {
		return nil
	}
	attempt, getErr := repository.Get(ctx, id)
	if getErr != nil {
		return getErr
	}
	if attempt.Status == execution.StatusCompleted {
		return execution.ErrAttemptCompleted
	}
	return execution.ErrInvalidAttempt
}

// CancellationRequested lets the owning runner poll the durable request.
func (repository *ExecutionRepository) CancellationRequested(
	ctx context.Context,
	id job.ID,
	runnerPID int,
) (bool, error) {
	attempt, err := repository.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if attempt.RunnerPID != runnerPID {
		return false, execution.ErrOwnerMismatch
	}
	if attempt.Status == execution.StatusCompleted {
		return false, execution.ErrAttemptCompleted
	}
	return attempt.CancelRequestedAt != nil, nil
}

// Complete atomically stores the attempt result and terminal job transition.
func (repository *ExecutionRepository) Complete(
	ctx context.Context,
	id job.ID,
	runnerPID int,
	completion execution.Completion,
) (job.Job, execution.Attempt, error) {
	if !id.Valid() {
		return job.Job{}, execution.Attempt{}, job.ErrInvalidID
	}
	if runnerPID <= 0 {
		return job.Job{}, execution.Attempt{}, execution.ErrInvalidAttempt
	}
	if err := completion.Validate(); err != nil {
		return job.Job{}, execution.Attempt{}, err
	}
	completion.At = completion.At.UTC()
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, execution.Attempt{}, fmt.Errorf("begin execution completion: %w", err)
	}
	defer transaction.Rollback()

	attempt, err := getAttempt(ctx, transaction, id)
	if err != nil {
		return job.Job{}, execution.Attempt{}, err
	}
	if attempt.RunnerPID != runnerPID {
		return job.Job{}, execution.Attempt{}, execution.ErrOwnerMismatch
	}
	if attempt.Status == execution.StatusCompleted {
		return job.Job{}, execution.Attempt{}, execution.ErrAttemptCompleted
	}
	if completion.At.Before(attempt.HeartbeatAt) {
		return job.Job{}, execution.Attempt{}, execution.ErrInvalidCompletion
	}
	current, err := queryJob(ctx, transaction, `SELECT `+jobColumns+` FROM jobs
		LEFT JOIN job_progress ON job_progress.job_id = jobs.id
		WHERE jobs.id = ?`, id)
	if err != nil {
		return job.Job{}, execution.Attempt{}, fmt.Errorf("load job for execution completion: %w", err)
	}
	if attempt.Status == execution.StatusStarting && completion.Kind() == execution.CompletionSucceeded {
		return job.Job{}, execution.Attempt{}, execution.ErrInvalidCompletion
	}
	if attempt.Status == execution.StatusStarting && current.State != job.StateStarting ||
		attempt.Status == execution.StatusRunning && current.State != job.StateRunning && current.State != job.StateCollecting {
		return job.Job{}, execution.Attempt{}, execution.ErrInvalidCompletion
	}
	from := current.State
	to := job.StateSucceeded
	failure := completion.Failure
	if completion.Cancelled {
		to = job.StateCancelled
	} else if failure != nil {
		to = job.StateFailed
	}
	exitCode := any(nil)
	if completion.ExitCode != nil {
		exitCode = *completion.ExitCode
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE job_executions
		SET status = ?, heartbeat_at_ns = ?, finished_at_ns = ?, exit_code = ?,
			termination_signal = ?, completion = ?
		WHERE job_id = ? AND runner_pid = ? AND status IN (?, ?)
	`,
		execution.StatusCompleted,
		completion.At.UnixNano(),
		completion.At.UnixNano(),
		exitCode,
		completion.TerminationSignal,
		completion.Kind(),
		id,
		runnerPID,
		execution.StatusStarting,
		execution.StatusRunning,
	)
	if err != nil {
		return job.Job{}, execution.Attempt{}, fmt.Errorf("store execution completion: %w", err)
	}
	if err := requireOneAffected(result, "store execution completion"); err != nil {
		return job.Job{}, execution.Attempt{}, err
	}
	transition := job.Transition{JobID: id, From: from, To: to, At: completion.At, Failure: failure}
	updatedJob, err := applyJobTransition(ctx, transaction, transition)
	if err != nil {
		return job.Job{}, execution.Attempt{}, err
	}
	if err := transaction.Commit(); err != nil {
		return job.Job{}, execution.Attempt{}, fmt.Errorf("commit execution completion: %w", err)
	}
	attempt.Status = execution.StatusCompleted
	attempt.HeartbeatAt = completion.At
	attempt.FinishedAt = timePointer(completion.At)
	attempt.ExitCode = cloneIntPointer(completion.ExitCode)
	attempt.TerminationSignal = completion.TerminationSignal
	attempt.Completion = completion.Kind()
	if err := attempt.Validate(); err != nil {
		return job.Job{}, execution.Attempt{}, err
	}
	return updatedJob, attempt, nil
}

// Get returns the durable execution attempt for id.
func (repository *ExecutionRepository) Get(ctx context.Context, id job.ID) (execution.Attempt, error) {
	if !id.Valid() {
		return execution.Attempt{}, job.ErrInvalidID
	}
	attempt, err := getAttempt(ctx, repository.database, id)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Attempt{}, fmt.Errorf("%w: %s", execution.ErrNotFound, id)
	}
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("get execution attempt: %w", err)
	}
	return attempt, nil
}

// ListActive returns runner-owned attempts that have not completed.
func (repository *ExecutionRepository) ListActive(ctx context.Context) ([]execution.Attempt, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT `+executionColumns+`
		FROM job_executions
		WHERE status IN (?, ?)
		ORDER BY claimed_at_ns, job_id
	`, execution.StatusStarting, execution.StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("list active executions: %w", err)
	}
	defer rows.Close()
	var attempts []execution.Attempt
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active execution: %w", err)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active executions: %w", err)
	}
	return attempts, nil
}

func (repository *ExecutionRepository) explainOwnershipFailure(
	ctx context.Context,
	id job.ID,
	runnerPID int,
) error {
	attempt, err := repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if attempt.RunnerPID != runnerPID {
		return execution.ErrOwnerMismatch
	}
	if attempt.Status == execution.StatusCompleted {
		return execution.ErrAttemptCompleted
	}
	return execution.ErrInvalidAttempt
}

type attemptQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getAttempt(ctx context.Context, queryer attemptQueryer, id job.ID) (execution.Attempt, error) {
	return scanAttempt(queryer.QueryRowContext(
		ctx,
		`SELECT `+executionColumns+` FROM job_executions WHERE job_id = ?`,
		id,
	))
}

func scanAttempt(scanner rowScanner) (execution.Attempt, error) {
	var (
		jobID             string
		status            string
		runnerPID         int
		processPID        sql.NullInt64
		claimedAtNS       int64
		startedAtNS       sql.NullInt64
		heartbeatAtNS     int64
		cancelRequestedNS sql.NullInt64
		finishedAtNS      sql.NullInt64
		exitCode          sql.NullInt64
		terminationSignal string
		completion        string
	)
	if err := scanner.Scan(
		&jobID,
		&status,
		&runnerPID,
		&processPID,
		&claimedAtNS,
		&startedAtNS,
		&heartbeatAtNS,
		&cancelRequestedNS,
		&finishedAtNS,
		&exitCode,
		&terminationSignal,
		&completion,
	); err != nil {
		return execution.Attempt{}, err
	}
	id, err := job.ParseID(jobID)
	if err != nil {
		return execution.Attempt{}, err
	}
	attempt := execution.Attempt{
		JobID:             id,
		Status:            execution.Status(status),
		RunnerPID:         runnerPID,
		ProcessPID:        int(processPID.Int64),
		ClaimedAt:         time.Unix(0, claimedAtNS).UTC(),
		StartedAt:         nullableTime(startedAtNS),
		HeartbeatAt:       time.Unix(0, heartbeatAtNS).UTC(),
		CancelRequestedAt: nullableTime(cancelRequestedNS),
		FinishedAt:        nullableTime(finishedAtNS),
		TerminationSignal: terminationSignal,
		Completion:        execution.CompletionKind(completion),
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		attempt.ExitCode = &value
	}
	if err := attempt.Validate(); err != nil {
		return execution.Attempt{}, err
	}
	return attempt, nil
}

func requireActiveOwner(attempt execution.Attempt, runnerPID int, status execution.Status) error {
	if attempt.RunnerPID != runnerPID {
		return execution.ErrOwnerMismatch
	}
	if attempt.Status == execution.StatusCompleted {
		return execution.ErrAttemptCompleted
	}
	if attempt.Status != status {
		return execution.ErrInvalidAttempt
	}
	return nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	return timePointer(time.Unix(0, value.Int64).UTC())
}

func timePointer(value time.Time) *time.Time {
	copy := value.UTC()
	return &copy
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func requireOneAffected(result sql.Result, operation string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count rows for %s: %w", operation, err)
	}
	if count != 1 {
		return fmt.Errorf("%w: %s updated %d rows", job.ErrConflict, operation, count)
	}
	return nil
}
