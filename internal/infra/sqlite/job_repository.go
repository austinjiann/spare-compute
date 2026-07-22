package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

const (
	defaultListLimit = 100
	maximumListLimit = 500
)

var ErrInvalidListOptions = errors.New("invalid job list options")

const jobColumns = `
	jobs.id,
	jobs.executable,
	jobs.arguments_json,
	jobs.working_directory,
	jobs.environment_json,
	jobs.executor,
	jobs.container_image,
	jobs.outputs_json,
	jobs.state,
	jobs.created_at_ns,
	jobs.updated_at_ns,
	jobs.failure_code,
	jobs.failure_message,
	jobs.failure_retryable,
	job_progress.phase,
	job_progress.completed_bytes,
	job_progress.total_bytes,
	job_progress.updated_at_ns
`

// JobRepository persists jobs and transitions in SQLite.
type JobRepository struct {
	database *sql.DB
}

var _ job.Repository = (*JobRepository)(nil)
var _ job.ProgressRepository = (*JobRepository)(nil)

// Create inserts a validated job. Duplicate IDs return job.ErrConflict.
func (repository *JobRepository) Create(ctx context.Context, value job.Job) error {
	if err := value.Validate(); err != nil {
		return err
	}
	if value.State != job.StateCreated {
		return fmt.Errorf("%w: new repository job must be in %s state", job.ErrInvalidJob, job.StateCreated)
	}

	arguments, environment, outputs, err := encodeSpecCollections(value.Spec)
	if err != nil {
		return err
	}
	failureCode, failureMessage, failureRetryable := encodeFailure(value.Failure)

	_, err = repository.database.ExecContext(ctx, `
		INSERT INTO jobs (
			id,
			executable,
			arguments_json,
			working_directory,
			environment_json,
			executor,
			container_image,
			outputs_json,
			state,
			created_at_ns,
			updated_at_ns,
			failure_code,
			failure_message,
			failure_retryable
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		value.ID,
		value.Spec.Executable,
		arguments,
		value.Spec.WorkingDirectory,
		environment,
		value.Spec.Executor,
		value.Spec.ContainerImage,
		outputs,
		value.State,
		value.CreatedAt.UTC().UnixNano(),
		value.UpdatedAt.UTC().UnixNano(),
		failureCode,
		failureMessage,
		failureRetryable,
	)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: create job %s", job.ErrConflict, value.ID)
		}
		return fmt.Errorf("create job %s: %w", value.ID, err)
	}
	return nil
}

// Get loads and validates a job by ID.
func (repository *JobRepository) Get(ctx context.Context, id job.ID) (job.Job, error) {
	if !id.Valid() {
		return job.Job{}, job.ErrInvalidID
	}

	value, err := queryJob(ctx, repository.database, `SELECT `+jobColumns+` FROM jobs
		LEFT JOIN job_progress ON job_progress.job_id = jobs.id
		WHERE jobs.id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, fmt.Errorf("%w: %s", job.ErrNotFound, id)
	}
	if err != nil {
		return job.Job{}, fmt.Errorf("get job %s: %w", id, err)
	}
	return value, nil
}

// List returns newest jobs first, with deterministic ID tie-breaking.
func (repository *JobRepository) List(ctx context.Context, options job.ListOptions) ([]job.Job, error) {
	limit, err := normalizeListOptions(options)
	if err != nil {
		return nil, err
	}

	query := `SELECT ` + jobColumns + ` FROM jobs
		LEFT JOIN job_progress ON job_progress.job_id = jobs.id`
	arguments := make([]any, 0, len(options.States)+1)
	if len(options.States) > 0 {
		placeholders := make([]string, len(options.States))
		for index, state := range options.States {
			placeholders[index] = "?"
			arguments = append(arguments, state)
		}
		query += ` WHERE jobs.state IN (` + strings.Join(placeholders, ", ") + `)`
	}
	query += ` ORDER BY jobs.updated_at_ns DESC, jobs.id ASC LIMIT ?`
	arguments = append(arguments, limit)

	rows, err := repository.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	result := make([]job.Job, 0)
	for rows.Next() {
		value, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed job: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}
	return result, nil
}

// SetProgress upserts the latest byte-level progress for a local or remote job.
func (repository *JobRepository) SetProgress(ctx context.Context, id job.ID, progress job.Progress) error {
	if repository == nil || repository.database == nil {
		return errors.New("job repository is required")
	}
	if !id.Valid() {
		return job.ErrInvalidID
	}
	if err := progress.Validate(); err != nil {
		return err
	}
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO job_progress (
			job_id, phase, completed_bytes, total_bytes, updated_at_ns
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (job_id) DO UPDATE SET
			phase = excluded.phase,
			completed_bytes = excluded.completed_bytes,
			total_bytes = excluded.total_bytes,
			updated_at_ns = excluded.updated_at_ns
	`, id, progress.Phase, progress.CompletedBytes, progress.TotalBytes, progress.UpdatedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("set job progress: %w", err)
	}
	return nil
}

// GetProgress returns the latest progress for a local or remote job.
func (repository *JobRepository) GetProgress(ctx context.Context, id job.ID) (*job.Progress, error) {
	if repository == nil || repository.database == nil {
		return nil, errors.New("job repository is required")
	}
	if !id.Valid() {
		return nil, job.ErrInvalidID
	}
	var (
		phase          string
		completedBytes int64
		totalBytes     int64
		updatedAtNS    int64
	)
	err := repository.database.QueryRowContext(ctx, `
		SELECT phase, completed_bytes, total_bytes, updated_at_ns
		FROM job_progress
		WHERE job_id = ?
	`, id).Scan(&phase, &completedBytes, &totalBytes, &updatedAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job progress: %w", err)
	}
	return decodeProgressValues(phase, completedBytes, totalBytes, updatedAtNS)
}

// ClearProgress removes stale progress once an operation has completed.
func (repository *JobRepository) ClearProgress(ctx context.Context, id job.ID) error {
	if repository == nil || repository.database == nil {
		return errors.New("job repository is required")
	}
	if !id.Valid() {
		return job.ErrInvalidID
	}
	if _, err := repository.database.ExecContext(
		ctx, "DELETE FROM job_progress WHERE job_id = ?", id,
	); err != nil {
		return fmt.Errorf("clear job progress: %w", err)
	}
	return nil
}

// ApplyTransition atomically checks the source state, updates the job, and
// appends the durable transition record.
func (repository *JobRepository) ApplyTransition(ctx context.Context, transition job.Transition) (job.Job, error) {
	if !transition.JobID.Valid() {
		return job.Job{}, job.ErrInvalidID
	}
	if err := job.ValidateTransition(transition.From, transition.To); err != nil {
		return job.Job{}, err
	}

	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return job.Job{}, fmt.Errorf("begin job transition: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	next, err := applyJobTransition(ctx, transaction, transition)
	if err != nil {
		return job.Job{}, err
	}
	if err := transaction.Commit(); err != nil {
		return job.Job{}, fmt.Errorf("commit job transition: %w", err)
	}
	return next, nil
}

func applyJobTransition(
	ctx context.Context,
	transaction *sql.Tx,
	transition job.Transition,
) (job.Job, error) {
	current, err := queryJob(
		ctx,
		transaction,
		`SELECT `+jobColumns+` FROM jobs
		LEFT JOIN job_progress ON job_progress.job_id = jobs.id
		WHERE jobs.id = ?`,
		transition.JobID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, fmt.Errorf("%w: %s", job.ErrNotFound, transition.JobID)
	}
	if err != nil {
		return job.Job{}, fmt.Errorf("load job for transition: %w", err)
	}
	if current.State != transition.From {
		return job.Job{}, fmt.Errorf(
			"%w: job %s is %s, transition expected %s",
			job.ErrConflict,
			transition.JobID,
			current.State,
			transition.From,
		)
	}

	next, accepted, err := current.Apply(transition.To, transition.At, transition.Failure)
	if err != nil {
		return job.Job{}, err
	}
	failureCode, failureMessage, failureRetryable := encodeFailure(next.Failure)

	result, err := transaction.ExecContext(ctx, `
		UPDATE jobs
		SET state = ?,
			updated_at_ns = ?,
			failure_code = ?,
			failure_message = ?,
			failure_retryable = ?
		WHERE id = ? AND state = ? AND updated_at_ns = ?
	`,
		next.State,
		next.UpdatedAt.UTC().UnixNano(),
		failureCode,
		failureMessage,
		failureRetryable,
		current.ID,
		current.State,
		current.UpdatedAt.UTC().UnixNano(),
	)
	if err != nil {
		return job.Job{}, fmt.Errorf("update job transition: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return job.Job{}, fmt.Errorf("count transitioned jobs: %w", err)
	}
	if updated != 1 {
		return job.Job{}, fmt.Errorf("%w: job %s changed during transition", job.ErrConflict, current.ID)
	}

	transitionFailureCode, transitionFailureMessage, transitionFailureRetryable := encodeFailure(accepted.Failure)
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO job_transitions (
			job_id,
			from_state,
			to_state,
			at_ns,
			failure_code,
			failure_message,
			failure_retryable
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		accepted.JobID,
		accepted.From,
		accepted.To,
		accepted.At.UTC().UnixNano(),
		transitionFailureCode,
		transitionFailureMessage,
		transitionFailureRetryable,
	); err != nil {
		return job.Job{}, fmt.Errorf("record job transition: %w", err)
	}

	return next, nil
}

type rowScanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryJob(ctx context.Context, queryer rowQueryer, query string, arguments ...any) (job.Job, error) {
	return scanJob(queryer.QueryRowContext(ctx, query, arguments...))
}

func scanJob(scanner rowScanner) (job.Job, error) {
	var (
		id               string
		executable       string
		argumentsJSON    string
		workingDirectory string
		environmentJSON  string
		executor         string
		containerImage   string
		outputsJSON      string
		state            string
		createdAtNS      int64
		updatedAtNS      int64
		failureCode      sql.NullString
		failureMessage   sql.NullString
		failureRetryable sql.NullBool
		progressPhase    sql.NullString
		progressDone     sql.NullInt64
		progressTotal    sql.NullInt64
		progressUpdated  sql.NullInt64
	)

	if err := scanner.Scan(
		&id,
		&executable,
		&argumentsJSON,
		&workingDirectory,
		&environmentJSON,
		&executor,
		&containerImage,
		&outputsJSON,
		&state,
		&createdAtNS,
		&updatedAtNS,
		&failureCode,
		&failureMessage,
		&failureRetryable,
		&progressPhase,
		&progressDone,
		&progressTotal,
		&progressUpdated,
	); err != nil {
		return job.Job{}, err
	}

	parsedID, err := job.ParseID(id)
	if err != nil {
		return job.Job{}, fmt.Errorf("decode job ID: %w", err)
	}
	parsedState, err := job.ParseState(state)
	if err != nil {
		return job.Job{}, fmt.Errorf("decode job state: %w", err)
	}

	var arguments []string
	if err := json.Unmarshal([]byte(argumentsJSON), &arguments); err != nil {
		return job.Job{}, fmt.Errorf("decode job arguments: %w", err)
	}
	var environment map[string]string
	if err := json.Unmarshal([]byte(environmentJSON), &environment); err != nil {
		return job.Job{}, fmt.Errorf("decode job environment: %w", err)
	}
	var outputs []string
	if err := json.Unmarshal([]byte(outputsJSON), &outputs); err != nil {
		return job.Job{}, fmt.Errorf("decode job outputs: %w", err)
	}

	failure, err := decodeFailure(failureCode, failureMessage, failureRetryable)
	if err != nil {
		return job.Job{}, err
	}
	progress, err := decodeProgress(progressPhase, progressDone, progressTotal, progressUpdated)
	if err != nil {
		return job.Job{}, err
	}

	value := job.Job{
		ID: parsedID,
		Spec: job.Spec{
			Executable:       executable,
			Arguments:        arguments,
			WorkingDirectory: workingDirectory,
			Environment:      environment,
			Executor:         job.Executor(executor),
			ContainerImage:   containerImage,
			Outputs:          outputs,
		},
		State:     parsedState,
		CreatedAt: time.Unix(0, createdAtNS).UTC(),
		UpdatedAt: time.Unix(0, updatedAtNS).UTC(),
		Failure:   failure,
		Progress:  progress,
	}
	if err := value.Validate(); err != nil {
		return job.Job{}, fmt.Errorf("validate stored job: %w", err)
	}
	return value, nil
}

func decodeProgress(
	phase sql.NullString,
	completedBytes sql.NullInt64,
	totalBytes sql.NullInt64,
	updatedAtNS sql.NullInt64,
) (*job.Progress, error) {
	if !phase.Valid && !completedBytes.Valid && !totalBytes.Valid && !updatedAtNS.Valid {
		return nil, nil
	}
	if !phase.Valid || !completedBytes.Valid || !totalBytes.Valid || !updatedAtNS.Valid {
		return nil, errors.New("stored job has incomplete progress fields")
	}
	return decodeProgressValues(phase.String, completedBytes.Int64, totalBytes.Int64, updatedAtNS.Int64)
}

func decodeProgressValues(
	phaseValue string,
	completedBytes int64,
	totalBytes int64,
	updatedAtNS int64,
) (*job.Progress, error) {
	phase, err := job.ParseProgressPhase(phaseValue)
	if err != nil {
		return nil, err
	}
	progress := &job.Progress{
		Phase: phase, CompletedBytes: completedBytes, TotalBytes: totalBytes,
		UpdatedAt: time.Unix(0, updatedAtNS).UTC(),
	}
	if err := progress.Validate(); err != nil {
		return nil, err
	}
	return progress, nil
}

func encodeSpecCollections(spec job.Spec) (string, string, string, error) {
	arguments, err := json.Marshal(spec.Arguments)
	if err != nil {
		return "", "", "", fmt.Errorf("encode job arguments: %w", err)
	}
	environment, err := json.Marshal(spec.Environment)
	if err != nil {
		return "", "", "", fmt.Errorf("encode job environment: %w", err)
	}
	outputs, err := json.Marshal(spec.Outputs)
	if err != nil {
		return "", "", "", fmt.Errorf("encode job outputs: %w", err)
	}
	return string(arguments), string(environment), string(outputs), nil
}

func encodeFailure(failure *job.Failure) (any, any, any) {
	if failure == nil {
		return nil, nil, nil
	}
	return failure.Code, failure.Message, failure.Retryable
}

func decodeFailure(code, message sql.NullString, retryable sql.NullBool) (*job.Failure, error) {
	if !code.Valid && !message.Valid && !retryable.Valid {
		return nil, nil
	}
	if !code.Valid || !message.Valid || !retryable.Valid {
		return nil, errors.New("stored job has incomplete failure fields")
	}
	return &job.Failure{
		Code:      code.String,
		Message:   message.String,
		Retryable: retryable.Bool,
	}, nil
}

func normalizeListOptions(options job.ListOptions) (int, error) {
	if options.Limit < 0 {
		return 0, fmt.Errorf("%w: limit cannot be negative", ErrInvalidListOptions)
	}
	if options.Limit > maximumListLimit {
		return 0, fmt.Errorf(
			"%w: limit cannot exceed %d",
			ErrInvalidListOptions,
			maximumListLimit,
		)
	}
	for _, state := range options.States {
		if !state.Valid() {
			return 0, fmt.Errorf("%w: %w: %q", ErrInvalidListOptions, job.ErrInvalidState, state)
		}
	}

	limit := options.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	return limit, nil
}

type errorWithCode interface {
	Code() int
}

func isConstraintError(err error) bool {
	var coded errorWithCode
	return errors.As(err, &coded) && coded.Code()&0xff == 19
}
