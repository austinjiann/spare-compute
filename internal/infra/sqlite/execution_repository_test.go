package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

func TestExecutionRepositorySuccessfulLifecycle(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 1)
	repository := database.Executions()

	claimed, err := repository.Claim(ctx, value.ID, 100, value.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Status != execution.StatusStarting || claimed.RunnerPID != 100 {
		t.Fatalf("Claim() = %#v", claimed)
	}
	assertStoredJobState(t, ctx, database, value.ID, job.StateStarting)

	startedAt := claimed.ClaimedAt.Add(time.Second)
	running, err := repository.MarkRunning(ctx, value.ID, 100, 101, startedAt)
	if err != nil {
		t.Fatalf("MarkRunning() error = %v", err)
	}
	if running.Status != execution.StatusRunning || running.ProcessPID != 101 {
		t.Fatalf("MarkRunning() = %#v", running)
	}
	assertStoredJobState(t, ctx, database, value.ID, job.StateRunning)

	heartbeatAt := startedAt.Add(time.Second)
	if err := repository.Heartbeat(ctx, value.ID, 100, heartbeatAt); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	exitCode := 0
	completedJob, completed, err := repository.Complete(ctx, value.ID, 100, execution.Completion{
		At:       heartbeatAt.Add(time.Second),
		ExitCode: &exitCode,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completedJob.State != job.StateSucceeded || completed.Completion != execution.CompletionSucceeded {
		t.Fatalf("Complete() = job %#v, attempt %#v", completedJob, completed)
	}
	assertStoredJobState(t, ctx, database, value.ID, job.StateSucceeded)
	if _, err := repository.CancellationRequested(ctx, value.ID, 100); !errors.Is(err, execution.ErrAttemptCompleted) {
		t.Fatalf("CancellationRequested(completed) error = %v", err)
	}
}

func TestExecutionRepositoryCancellationIsDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 2)
	repository := database.Executions()
	claimed, err := repository.Claim(ctx, value.ID, 200, value.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	running, err := repository.MarkRunning(ctx, value.ID, 200, 201, claimed.ClaimedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := running.HeartbeatAt.Add(time.Second)
	if err := repository.RequestCancellation(ctx, value.ID, requestedAt); err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	if err := repository.RequestCancellation(ctx, value.ID, requestedAt.Add(time.Second)); err != nil {
		t.Fatalf("second RequestCancellation() error = %v", err)
	}
	requested, err := repository.CancellationRequested(ctx, value.ID, 200)
	if err != nil || !requested {
		t.Fatalf("CancellationRequested() = %v, %v", requested, err)
	}
	exitCode := -1
	completedJob, completed, err := repository.Complete(ctx, value.ID, 200, execution.Completion{
		At:                requestedAt.Add(2 * time.Second),
		ExitCode:          &exitCode,
		TerminationSignal: "terminated",
		Cancelled:         true,
	})
	if err != nil {
		t.Fatalf("Complete(cancelled) error = %v", err)
	}
	if completedJob.State != job.StateCancelled || completed.Completion != execution.CompletionCancelled {
		t.Fatalf("cancelled result = job %s, completion %s", completedJob.State, completed.Completion)
	}
}

func TestExecutionRepositoryRecordsStartFailure(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 3)
	repository := database.Executions()
	claimed, err := repository.Claim(ctx, value.ID, 300, value.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	failure := &job.Failure{Code: "process_start", Message: "executable not found"}
	completedJob, completed, err := repository.Complete(ctx, value.ID, 300, execution.Completion{
		At:      claimed.ClaimedAt.Add(time.Second),
		Failure: failure,
	})
	if err != nil {
		t.Fatalf("Complete(start failure) error = %v", err)
	}
	if completedJob.State != job.StateFailed || !reflect.DeepEqual(completedJob.Failure, failure) {
		t.Fatalf("failed job = %#v", completedJob)
	}
	if completed.ProcessPID != 0 || completed.StartedAt != nil {
		t.Fatalf("start failure contains process metadata: %#v", completed)
	}
}

func TestExecutionRepositoryAllowsOnlyOneClaim(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 4)
	repository := database.Executions()
	errorsByRunner := make(chan error, 2)
	var wait sync.WaitGroup
	for runnerPID := 400; runnerPID < 402; runnerPID++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := repository.Claim(ctx, value.ID, runnerPID, value.UpdatedAt.Add(time.Second))
			errorsByRunner <- err
		}()
	}
	wait.Wait()
	close(errorsByRunner)
	var succeeded, rejected int
	for err := range errorsByRunner {
		if err == nil {
			succeeded++
		} else if errors.Is(err, execution.ErrNotClaimable) {
			rejected++
		} else {
			t.Fatalf("Claim() error = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("claims = %d succeeded, %d rejected", succeeded, rejected)
	}
}

func TestExecutionRepositoryRejectsWrongRunner(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 5)
	repository := database.Executions()
	claimed, err := repository.Claim(ctx, value.ID, 500, value.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.MarkRunning(
		ctx,
		value.ID,
		501,
		502,
		claimed.ClaimedAt.Add(time.Second),
	); !errors.Is(err, execution.ErrOwnerMismatch) {
		t.Fatalf("MarkRunning(wrong owner) error = %v", err)
	}
}

func TestExecutionRepositoryLogMetadata(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := createQueuedJob(t, ctx, database, 6)
	repository := database.Executions()
	if _, err := repository.Claim(ctx, value.ID, 600, value.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	cursor, err := repository.Cursor(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.DataOffset != 0 || cursor.NextSequence != 1 {
		t.Fatalf("initial cursor = %#v", cursor)
	}
	first, err := repository.Commit(ctx, value.ID, 0, joblogging.StreamStdout, 5, testTime(7))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Commit(ctx, value.ID, 5, joblogging.StreamStderr, 4, testTime(8))
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d", first.Sequence, second.Sequence)
	}
	if _, err := repository.Commit(
		ctx,
		value.ID,
		0,
		joblogging.StreamStdout,
		1,
		testTime(9),
	); !errors.Is(err, joblogging.ErrConflict) {
		t.Fatalf("Commit(stale offset) error = %v", err)
	}

	page, hasMore, err := repository.List(ctx, value.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Sequence != 1 || !hasMore {
		t.Fatalf("first page = %#v, hasMore %v", page, hasMore)
	}
	page, hasMore, err = repository.List(ctx, value.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Sequence != 2 || hasMore {
		t.Fatalf("second page = %#v, hasMore %v", page, hasMore)
	}
}

func createQueuedJob(t *testing.T, ctx context.Context, database *Database, sequence int) job.Job {
	t.Helper()
	value := newTestJob(t, sequence, testTime(sequence))
	if err := database.Jobs().Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	value = applyStoredTransition(t, ctx, database.Jobs(), value, job.StateValidating, nil)
	value = applyStoredTransition(t, ctx, database.Jobs(), value, job.StateQueued, nil)
	return value
}

func assertStoredJobState(t *testing.T, ctx context.Context, database *Database, id job.ID, state job.State) {
	t.Helper()
	value, err := database.Jobs().Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if value.State != state {
		t.Fatalf("stored job state = %s, want %s", value.State, state)
	}
}
