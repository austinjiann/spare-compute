package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

func TestJobRepositoryPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computehop.db")
	want := newTestJob(t, 1, testTime(1))

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := first.Jobs().Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer second.Close()

	got, err := second.Jobs().Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %#v, want %#v", got, want)
	}
}

func TestJobRepositoryRejectsDuplicateID(t *testing.T) {
	ctx := context.Background()
	repository := openTestDatabase(t).Jobs()
	value := newTestJob(t, 1, testTime(1))

	if err := repository.Create(ctx, value); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if err := repository.Create(ctx, value); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("second Create() error = %v, want job.ErrConflict", err)
	}
}

func TestJobRepositoryAppliesTransitionAndRecordsHistory(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	repository := database.Jobs()
	current := newTestJob(t, 1, testTime(1))
	if err := repository.Create(ctx, current); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	next, transition, err := current.Apply(job.StateValidating, current.UpdatedAt.Add(time.Second), nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	stored, err := repository.ApplyTransition(ctx, transition)
	if err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if !reflect.DeepEqual(stored, next) {
		t.Fatalf("ApplyTransition() = %#v, want %#v", stored, next)
	}

	loaded, err := repository.Get(ctx, current.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, next) {
		t.Fatalf("persisted job = %#v, want %#v", loaded, next)
	}

	var count int
	if err := database.sql.QueryRow(
		"SELECT COUNT(*) FROM job_transitions WHERE job_id = ?",
		current.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if count != 1 {
		t.Fatalf("transition count = %d, want 1", count)
	}
}

func TestJobRepositoryAllowsOnlyOneConcurrentTransition(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	repository := database.Jobs()
	current := newTestJob(t, 1, testTime(1))
	if err := repository.Create(ctx, current); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, transition, err := current.Apply(job.StateValidating, current.UpdatedAt.Add(time.Second), nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	errorsByAttempt := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, transitionErr := repository.ApplyTransition(ctx, transition)
			errorsByAttempt <- transitionErr
		}()
	}
	wait.Wait()
	close(errorsByAttempt)

	var succeeded, conflicted int
	for attemptErr := range errorsByAttempt {
		switch {
		case attemptErr == nil:
			succeeded++
		case errors.Is(attemptErr, job.ErrConflict):
			conflicted++
		default:
			t.Fatalf("ApplyTransition() unexpected error = %v", attemptErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("transition results = %d succeeded, %d conflicted; want 1 and 1", succeeded, conflicted)
	}

	var count int
	if err := database.sql.QueryRow(
		"SELECT COUNT(*) FROM job_transitions WHERE job_id = ?",
		current.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if count != 1 {
		t.Fatalf("transition count = %d, want 1", count)
	}
}

func TestJobRepositoryPersistsStructuredFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computehop.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	repository := database.Jobs()
	current := newTestJob(t, 1, testTime(1))
	if err := repository.Create(ctx, current); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for _, state := range []job.State{
		job.StateValidating,
		job.StateQueued,
		job.StateStarting,
		job.StateRunning,
	} {
		current = applyStoredTransition(t, ctx, repository, current, state, nil)
	}
	failure := &job.Failure{
		Code:      "process_exit",
		Message:   "process exited with status 1",
		Retryable: false,
	}
	current = applyStoredTransition(t, ctx, repository, current, job.StateFailed, failure)
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	database, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer database.Close()
	repository = database.Jobs()
	loaded, err := repository.Get(ctx, current.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Failure, failure) {
		t.Fatalf("failure = %#v, want %#v", loaded.Failure, failure)
	}
}

func TestJobRepositoryListsNewestAndFiltersState(t *testing.T) {
	ctx := context.Background()
	repository := openTestDatabase(t).Jobs()
	first := newTestJob(t, 1, testTime(1))
	second := newTestJob(t, 2, testTime(2))
	third := newTestJob(t, 3, testTime(3))
	for _, value := range []job.Job{first, second, third} {
		if err := repository.Create(ctx, value); err != nil {
			t.Fatalf("Create(%s) error = %v", value.ID, err)
		}
	}
	first = applyStoredTransition(t, ctx, repository, first, job.StateValidating, nil)

	newest, err := repository.List(ctx, job.ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got, want := jobIDs(newest), []job.ID{third.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List() IDs = %v, want %v", got, want)
	}

	validating, err := repository.List(ctx, job.ListOptions{States: []job.State{job.StateValidating}})
	if err != nil {
		t.Fatalf("filtered List() error = %v", err)
	}
	if got, want := jobIDs(validating), []job.ID{first.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered List() IDs = %v, want %v", got, want)
	}
}

func TestJobRepositoryValidatesInputs(t *testing.T) {
	ctx := context.Background()
	repository := openTestDatabase(t).Jobs()

	invalid := newTestJob(t, 1, testTime(1))
	invalid.State = job.StateRunning
	if err := repository.Create(ctx, invalid); !errors.Is(err, job.ErrInvalidJob) {
		t.Fatalf("Create(non-created job) error = %v, want job.ErrInvalidJob", err)
	}

	if _, err := repository.Get(ctx, "bad-id"); !errors.Is(err, job.ErrInvalidID) {
		t.Fatalf("Get(bad ID) error = %v, want job.ErrInvalidID", err)
	}

	missingID := mustJobID(t, 99)
	if _, err := repository.Get(ctx, missingID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get(missing ID) error = %v, want job.ErrNotFound", err)
	}

	if _, err := repository.List(ctx, job.ListOptions{Limit: -1}); !errors.Is(err, ErrInvalidListOptions) {
		t.Fatalf("List(negative limit) error = %v, want ErrInvalidListOptions", err)
	}
	if _, err := repository.List(ctx, job.ListOptions{Limit: maximumListLimit + 1}); !errors.Is(err, ErrInvalidListOptions) {
		t.Fatalf("List(large limit) error = %v, want ErrInvalidListOptions", err)
	}
	if _, err := repository.List(ctx, job.ListOptions{States: []job.State{"paused"}}); !errors.Is(err, job.ErrInvalidState) {
		t.Fatalf("List(invalid state) error = %v, want job.ErrInvalidState", err)
	}

	_, err := repository.ApplyTransition(ctx, job.Transition{
		JobID: missingID,
		From:  "paused",
		To:    job.StateRunning,
		At:    testTime(1),
	})
	if !errors.Is(err, job.ErrInvalidState) {
		t.Fatalf("ApplyTransition(invalid state) error = %v, want job.ErrInvalidState", err)
	}
}

func openTestDatabase(t *testing.T) *Database {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "computehop.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func newTestJob(t *testing.T, sequence int, createdAt time.Time) job.Job {
	t.Helper()
	value, err := job.New(
		mustJobID(t, sequence),
		job.Spec{
			Executable:       "cargo",
			Arguments:        []string{"build", "--release"},
			WorkingDirectory: "project",
			Environment:      map[string]string{"CARGO_TERM_COLOR": "always"},
			Executor:         job.ExecutorNative,
		},
		createdAt,
	)
	if err != nil {
		t.Fatalf("job.New() error = %v", err)
	}
	return value
}

func mustJobID(t *testing.T, sequence int) job.ID {
	t.Helper()
	value := fmt.Sprintf("019abcdf-0123-4567-89ab-%012x", sequence)
	id, err := job.ParseID(value)
	if err != nil {
		t.Fatalf("job.ParseID(%q) error = %v", value, err)
	}
	return id
}

func testTime(hour int) time.Time {
	return time.Date(2026, time.July, 18, hour, 0, 0, 0, time.UTC)
}

func applyStoredTransition(
	t *testing.T,
	ctx context.Context,
	repository *JobRepository,
	current job.Job,
	to job.State,
	failure *job.Failure,
) job.Job {
	t.Helper()
	expected, transition, err := current.Apply(to, current.UpdatedAt.Add(time.Second), failure)
	if err != nil {
		t.Fatalf("Job.Apply(%s) error = %v", to, err)
	}
	stored, err := repository.ApplyTransition(ctx, transition)
	if err != nil {
		t.Fatalf("ApplyTransition(%s) error = %v", to, err)
	}
	if !reflect.DeepEqual(stored, expected) {
		t.Fatalf("ApplyTransition(%s) = %#v, want %#v", to, stored, expected)
	}
	return stored
}

func jobIDs(jobs []job.Job) []job.ID {
	result := make([]job.ID, len(jobs))
	for index, value := range jobs {
		result[index] = value.ID
	}
	return result
}
