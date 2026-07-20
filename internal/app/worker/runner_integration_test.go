//go:build darwin || linux

package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
)

func TestRunnerExecutesAndPersistsOutput(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable:       "/bin/sh",
		Arguments:        []string{"-c", "printf output; printf problem >&2"},
		WorkingDirectory: t.TempDir(),
		Executor:         job.ExecutorNative,
	})

	if err := harness.runner.Run(context.Background(), harness.job.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	completed, err := harness.database.Jobs().Get(context.Background(), harness.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != job.StateSucceeded {
		t.Fatalf("state = %s, want succeeded; failure = %#v", completed.State, completed.Failure)
	}
	page, err := harness.logs.Read(context.Background(), harness.job.ID, 0, joblogging.MaximumPageLimit)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	for _, record := range page.Records {
		destination := &stdout
		if record.Stream == joblogging.StreamStderr {
			destination = &stderr
		}
		_, _ = destination.Write(record.Data)
	}
	if stdout.String() != "output" || stderr.String() != "problem" {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunnerAcknowledgesCancellationAfterProcessStops(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable:       "/bin/sh",
		Arguments:        []string{"-c", "sleep 30"},
		WorkingDirectory: t.TempDir(),
		Executor:         job.ExecutorNative,
	})
	result := make(chan error, 1)
	go func() { result <- harness.runner.Run(context.Background(), harness.job.ID) }()
	waitForJobState(t, harness.database, harness.job.ID, job.StateRunning)

	requested, err := harness.service.Cancel(context.Background(), harness.job.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if requested.State != job.StateRunning {
		t.Fatalf("cancellation request state = %s, want running", requested.State)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop cancelled process")
	}
	waitForJobState(t, harness.database, harness.job.ID, job.StateCancelled)
}

func TestRunnerPersistsProcessStartFailure(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable:       filepath.Join(t.TempDir(), "missing-program"),
		WorkingDirectory: t.TempDir(),
		Executor:         job.ExecutorNative,
	})
	if err := harness.runner.Run(context.Background(), harness.job.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	failed, err := harness.database.Jobs().Get(context.Background(), harness.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != job.StateFailed || failed.Failure == nil || failed.Failure.Code != "process_start" {
		t.Fatalf("failed job = %#v", failed)
	}
}

type runnerHarness struct {
	database *sqlite.Database
	logs     *joblogging.Store
	service  *JobService
	runner   *Runner
	job      job.Job
}

func newRunnerHarness(t *testing.T, spec job.Spec) runnerHarness {
	t.Helper()
	ctx := context.Background()
	stateDir := t.TempDir()
	database, err := sqlite.Open(ctx, filepath.Join(stateDir, "computehop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	logs, err := joblogging.NewStore(stateDir, database.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewJobService(Dependencies{
		Jobs:       database.Jobs(),
		Executions: database.Executions(),
		Logs:       logs,
		GenerateID: job.NewID,
		Now:        time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := service.Submit(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(RunnerDependencies{
		Jobs:       database.Jobs(),
		Executions: database.Executions(),
		Logs:       logs,
		StartProcess: func(spec job.Spec, stdout, stderr io.Writer) (NativeProcess, error) {
			return processes.Start(spec, stdout, stderr)
		},
		RunnerPID:         os.Getpid,
		Now:               time.Now,
		HeartbeatInterval: 10 * time.Millisecond,
		StopGracePeriod:   100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runnerHarness{database: database, logs: logs, service: service, runner: runner, job: queued}
}

func waitForJobState(t *testing.T, database *sqlite.Database, id job.ID, want job.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := database.Jobs().Get(context.Background(), id)
		if err == nil && current.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := database.Jobs().Get(context.Background(), id)
	t.Fatalf("job state = %s, want %s", current.State, want)
}
