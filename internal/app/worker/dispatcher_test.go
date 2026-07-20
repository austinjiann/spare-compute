package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
)

func TestDispatcherLaunchesEachQueuedJobOnceWithinRetryWindow(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)
	queued, err := service.Submit(context.Background(), validSpec())
	if err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	now := queued.UpdatedAt.Add(time.Second)
	dispatcher, err := NewDispatcher(DispatcherDependencies{
		Jobs:            repository,
		Executions:      &fakeExecutionRepository{},
		Launcher:        launcher,
		ProcessAlive:    func(int) bool { return false },
		KillProcessTree: func(int) error { return nil },
		Now:             func() time.Time { return now },
		LaunchRetry:     time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.ids) != 1 || launcher.ids[0] != queued.ID {
		t.Fatalf("launched jobs = %v", launcher.ids)
	}
}

func TestDispatcherKillsOrphanedTreeAndFailsLostAttempt(t *testing.T) {
	claimed := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	started := claimed.Add(time.Second)
	id := mustJobID(t, 99)
	repository := &fakeExecutionRepository{active: []execution.Attempt{{
		JobID:       id,
		Status:      execution.StatusRunning,
		RunnerPID:   10,
		ProcessPID:  20,
		ClaimedAt:   claimed,
		StartedAt:   &started,
		HeartbeatAt: started,
	}}}
	var killed int
	dispatcher, err := NewDispatcher(DispatcherDependencies{
		Jobs:             newMemoryRepository(),
		Executions:       repository,
		Launcher:         &recordingLauncher{},
		ProcessAlive:     func(int) bool { return false },
		KillProcessTree:  func(pid int) error { killed = pid; return nil },
		Now:              func() time.Time { return started.Add(time.Minute) },
		RunnerStaleAfter: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if killed != 20 {
		t.Fatalf("killed PID = %d, want 20", killed)
	}
	if repository.completed.JobID != id || repository.completion.Failure == nil ||
		repository.completion.Failure.Code != "runner_lost" {
		t.Fatalf("completion = %#v for attempt %#v", repository.completion, repository.completed)
	}
}

type recordingLauncher struct {
	ids []job.ID
}

type fakeExecutionRepository struct {
	active     []execution.Attempt
	completed  execution.Attempt
	completion execution.Completion
}

func (fakeExecutionRepository) Claim(context.Context, job.ID, int, time.Time) (execution.Attempt, error) {
	return execution.Attempt{}, errors.New("not implemented")
}

func (fakeExecutionRepository) MarkRunning(context.Context, job.ID, int, int, time.Time) (execution.Attempt, error) {
	return execution.Attempt{}, errors.New("not implemented")
}

func (fakeExecutionRepository) Heartbeat(context.Context, job.ID, int, time.Time) error {
	return errors.New("not implemented")
}

func (fakeExecutionRepository) RequestCancellation(context.Context, job.ID, time.Time) error {
	return errors.New("not implemented")
}

func (fakeExecutionRepository) CancellationRequested(context.Context, job.ID, int) (bool, error) {
	return false, errors.New("not implemented")
}

func (repository *fakeExecutionRepository) Complete(
	_ context.Context,
	id job.ID,
	runnerPID int,
	completion execution.Completion,
) (job.Job, execution.Attempt, error) {
	for _, attempt := range repository.active {
		if attempt.JobID == id && attempt.RunnerPID == runnerPID {
			repository.completed = attempt
			repository.completion = completion
			return job.Job{}, attempt, nil
		}
	}
	return job.Job{}, execution.Attempt{}, execution.ErrNotFound
}

func (fakeExecutionRepository) Get(context.Context, job.ID) (execution.Attempt, error) {
	return execution.Attempt{}, execution.ErrNotFound
}

func (repository *fakeExecutionRepository) ListActive(context.Context) ([]execution.Attempt, error) {
	return append([]execution.Attempt(nil), repository.active...), nil
}

func (launcher *recordingLauncher) Launch(id job.ID) error {
	launcher.ids = append(launcher.ids, id)
	return nil
}
