// Package worker coordinates job use cases owned by a ComputeHop worker.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

var (
	ErrMissingDependency  = errors.New("worker job service dependency is required")
	ErrJobTerminal        = errors.New("job is already terminal")
	ErrSnapshotsDisabled  = errors.New("project snapshots are not configured on this worker")
	ErrSnapshotIncomplete = errors.New("project snapshot is missing content")
	ErrArtifactsDisabled  = errors.New("job artifacts are not configured on this worker")
	ErrArtifactsNotReady  = errors.New("job artifacts are not ready")
)

// Dependencies are explicit so production wiring and tests use the same
// constructor without global clocks or random generators.
type Dependencies struct {
	Jobs       job.Repository
	Executions ExecutionController
	Logs       LogReader
	GenerateID func() (job.ID, error)
	Now        func() time.Time
	Snapshots  SnapshotWorkspace
	Artifacts  ArtifactController
}

// ArtifactController owns collected bundles, authorized chunk reads, and local restoration.
type ArtifactController interface {
	Get(context.Context, job.ID) (artifact.Bundle, error)
	ReadJobChunk(context.Context, job.ID, snapshot.Digest) ([]byte, error)
	Restore(context.Context, artifact.Bundle, string) (artifact.RestoreResult, error)
	MarkRetrieved(context.Context, job.ID) error
}

// MarkArtifactsRetrieved releases uncollected-result protection only after the
// orchestrator confirms a complete verified restore.
func (service *JobService) MarkArtifactsRetrieved(ctx context.Context, id job.ID) error {
	if _, err := service.ReadArtifacts(ctx, id); err != nil {
		return err
	}
	return service.artifacts.MarkRetrieved(ctx, id)
}

// SnapshotWorkspace owns verified chunks and isolated job materialization.
type SnapshotWorkspace interface {
	Missing(context.Context, []snapshot.Digest) ([]snapshot.Digest, error)
	Put(context.Context, snapshot.Digest, []byte) error
	Materialize(context.Context, job.ID, snapshot.Manifest, string) (string, error)
	RemoveWorkspace(job.ID) error
}

type snapshotReservationWorkspace interface {
	Reserve(context.Context, snapshot.Digest, []snapshot.Digest) error
	ReleaseReservation(snapshot.Digest)
}

type snapshotUseWorkspace interface {
	BeginUse() func()
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
	snapshots  SnapshotWorkspace
	artifacts  ArtifactController
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
		snapshots:  dependencies.Snapshots,
		artifacts:  dependencies.Artifacts,
	}, nil
}

// JobArtifacts binds a completed job to its immutable output bundle.
type JobArtifacts struct {
	Job    job.Job
	Bundle artifact.Bundle
}

// ReadArtifacts returns a completed job's collected outputs.
func (service *JobService) ReadArtifacts(ctx context.Context, id job.ID) (JobArtifacts, error) {
	if service.artifacts == nil {
		return JobArtifacts{}, ErrArtifactsDisabled
	}
	current, err := service.jobs.Get(ctx, id)
	if err != nil {
		return JobArtifacts{}, err
	}
	if current.State != job.StateSucceeded || len(current.Spec.Outputs) == 0 {
		return JobArtifacts{}, fmt.Errorf("%w: job %s is %s", ErrArtifactsNotReady, id, current.State)
	}
	bundle, err := service.artifacts.Get(ctx, id)
	if err != nil {
		return JobArtifacts{}, err
	}
	if bundle.JobID != id {
		return JobArtifacts{}, artifact.ErrInvalidBundle
	}
	return JobArtifacts{Job: current, Bundle: bundle}, nil
}

// ReadArtifactChunk returns one chunk authorized by a completed job bundle.
func (service *JobService) ReadArtifactChunk(
	ctx context.Context,
	id job.ID,
	digest snapshot.Digest,
) ([]byte, error) {
	if _, err := service.ReadArtifacts(ctx, id); err != nil {
		return nil, err
	}
	return service.artifacts.ReadJobChunk(ctx, id, digest)
}

// RestoreArtifacts reconstructs local-job outputs without overwriting files.
func (service *JobService) RestoreArtifacts(
	ctx context.Context,
	id job.ID,
	destination string,
) (artifact.RestoreResult, error) {
	result, err := service.ReadArtifacts(ctx, id)
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	return service.artifacts.Restore(ctx, result.Bundle, destination)
}

// Submit validates and durably accepts a job into the worker queue.
func (service *JobService) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	id, err := service.generateID()
	if err != nil {
		return job.Job{}, fmt.Errorf("generate job ID: %w", err)
	}
	return service.submitWithID(ctx, id, spec)
}

// MissingChunks returns the content absent from this worker's verified cache.
func (service *JobService) MissingChunks(
	ctx context.Context,
	digests []snapshot.Digest,
) ([]snapshot.Digest, error) {
	if service.snapshots == nil {
		return nil, ErrSnapshotsDisabled
	}
	return service.snapshots.Missing(ctx, digests)
}

// PutChunk verifies and stores one uploaded project chunk.
func (service *JobService) PutChunk(
	ctx context.Context,
	digest snapshot.Digest,
	contents []byte,
) error {
	if service.snapshots == nil {
		return ErrSnapshotsDisabled
	}
	if !digest.Valid() || len(contents) == 0 || len(contents) > snapshot.MaximumChunkBytes ||
		snapshot.Sum(contents) != digest {
		return snapshot.ErrInvalidDigest
	}
	return service.snapshots.Put(ctx, digest, contents)
}

// ReserveSnapshot pins a batch while a manifest uploads over multiple calls.
func (service *JobService) ReserveSnapshot(
	ctx context.Context,
	manifestID snapshot.Digest,
	digests []snapshot.Digest,
) error {
	if service.snapshots == nil {
		return ErrSnapshotsDisabled
	}
	reservations, ok := service.snapshots.(snapshotReservationWorkspace)
	if !ok {
		return nil
	}
	return reservations.Reserve(ctx, manifestID, digests)
}

// ReleaseSnapshot releases an incoming manifest reservation.
func (service *JobService) ReleaseSnapshot(manifestID snapshot.Digest) {
	if reservations, ok := service.snapshots.(snapshotReservationWorkspace); ok {
		reservations.ReleaseReservation(manifestID)
	}
}

// SubmitSnapshot materializes one complete immutable project into a fresh job
// workspace before durably queueing the command.
func (service *JobService) SubmitSnapshot(
	ctx context.Context,
	spec job.Spec,
	manifest snapshot.Manifest,
	workingSubdirectory string,
) (job.Job, error) {
	if service.snapshots == nil {
		return job.Job{}, ErrSnapshotsDisabled
	}
	if err := spec.Validate(); err != nil {
		return job.Job{}, err
	}
	if err := manifest.Validate(); err != nil {
		return job.Job{}, err
	}
	if workingSubdirectory != "" {
		if err := snapshot.ValidatePath(workingSubdirectory); err != nil {
			return job.Job{}, err
		}
	}
	release := func() {}
	if boundary, ok := service.snapshots.(snapshotUseWorkspace); ok {
		release = boundary.BeginUse()
	}
	defer release()
	missing, err := service.snapshots.Missing(ctx, manifest.Digests())
	if err != nil {
		return job.Job{}, err
	}
	if len(missing) != 0 {
		return job.Job{}, fmt.Errorf("%w: %d chunks", ErrSnapshotIncomplete, len(missing))
	}
	id, err := service.generateID()
	if err != nil {
		return job.Job{}, fmt.Errorf("generate job ID: %w", err)
	}
	workingDirectory, err := service.snapshots.Materialize(ctx, id, manifest, workingSubdirectory)
	if err != nil {
		return job.Job{}, fmt.Errorf("materialize project snapshot: %w", err)
	}
	prepared := spec.Clone()
	prepared.WorkingDirectory = workingDirectory
	value, err := service.submitWithID(ctx, id, prepared)
	if err != nil {
		removeErr := service.snapshots.RemoveWorkspace(id)
		return job.Job{}, errors.Join(err, removeErr)
	}
	return value, nil
}

func (service *JobService) submitWithID(ctx context.Context, id job.ID, spec job.Spec) (job.Job, error) {
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
		if current.State == job.StateStarting || current.State == job.StateRunning ||
			current.State == job.StateCollecting {
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
