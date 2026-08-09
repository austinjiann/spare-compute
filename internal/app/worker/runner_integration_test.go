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

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/infra/cas"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
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

func TestRunnerCancelsWhileExecutorIsStarting(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable: "echo", Arguments: []string{"hello"}, Executor: job.ExecutorNative,
	})
	starter := &blockingExecutorStarter{started: make(chan StartRequest, 1)}
	harness.runner.startExecution = starter

	result := make(chan error, 1)
	go func() { result <- harness.runner.Run(context.Background(), harness.job.ID) }()
	request := <-starter.started
	if request.JobID != harness.job.ID || request.Progress == nil {
		t.Fatalf("start request = %#v", request)
	}

	requested, err := harness.service.Cancel(context.Background(), harness.job.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if requested.State != job.StateStarting {
		t.Fatalf("cancellation request state = %s, want starting", requested.State)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not cancel starting executor")
	}
	waitForJobState(t, harness.database, harness.job.ID, job.StateCancelled)
}

func TestRunnerCollectsDeclaredOutputsBeforeSucceeding(t *testing.T) {
	workspace := t.TempDir()
	harness := newRunnerHarness(t, job.Spec{
		Executable: "/bin/sh", Arguments: []string{"-c", "mkdir -p dist; printf artifact > dist/result.txt"},
		WorkingDirectory: workspace, Executor: job.ExecutorNative, Outputs: []string{"dist/result.txt"},
	})
	if err := harness.runner.Run(context.Background(), harness.job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := harness.database.Jobs().Get(context.Background(), harness.job.ID)
	if err != nil || completed.State != job.StateSucceeded {
		t.Fatalf("completed job = %#v, %v", completed, err)
	}
	bundle, err := harness.artifacts.Get(context.Background(), harness.job.ID)
	if err != nil || len(bundle.Manifest.Files) != 1 || bundle.Manifest.Files[0].Path != "dist/result.txt" {
		t.Fatalf("artifact bundle = %#v, %v", bundle, err)
	}
}

func TestRunnerFailsWhenDeclaredOutputIsMissing(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable: "/bin/sh", Arguments: []string{"-c", "exit 0"},
		WorkingDirectory: t.TempDir(), Executor: job.ExecutorNative, Outputs: []string{"missing.txt"},
	})
	if err := harness.runner.Run(context.Background(), harness.job.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := harness.database.Jobs().Get(context.Background(), harness.job.ID)
	if err != nil || completed.State != job.StateFailed || completed.Failure == nil ||
		completed.Failure.Code != "collect_artifacts" {
		t.Fatalf("completed job = %#v, %v", completed, err)
	}
}

func TestRunnerCancelsWhileCollectingDeclaredOutputs(t *testing.T) {
	harness := newRunnerHarness(t, job.Spec{
		Executable: "/bin/sh", Arguments: []string{"-c", "printf artifact > result.txt"},
		WorkingDirectory: t.TempDir(), Executor: job.ExecutorNative, Outputs: []string{"result.txt"},
	})
	collector := &blockingArtifactCollector{started: make(chan struct{})}
	harness.runner.artifacts = collector
	result := make(chan error, 1)
	go func() { result <- harness.runner.Run(context.Background(), harness.job.ID) }()
	select {
	case <-collector.started:
	case <-time.After(5 * time.Second):
		t.Fatal("artifact collection did not start")
	}
	waitForJobState(t, harness.database, harness.job.ID, job.StateCollecting)
	requested, err := harness.service.Cancel(context.Background(), harness.job.ID)
	if err != nil || requested.State != job.StateCollecting {
		t.Fatalf("Cancel() = %#v, %v", requested, err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop cancelled artifact collection")
	}
	waitForJobState(t, harness.database, harness.job.ID, job.StateCancelled)
}

type blockingArtifactCollector struct {
	started chan struct{}
}

func (collector *blockingArtifactCollector) Collect(
	ctx context.Context,
	_ job.Job,
) (artifact.Bundle, error) {
	close(collector.started)
	<-ctx.Done()
	return artifact.Bundle{}, ctx.Err()
}

type blockingExecutorStarter struct {
	started chan StartRequest
}

func (*blockingExecutorStarter) SupportedExecutors() []job.Executor {
	return []job.Executor{job.ExecutorNative}
}

func (starter *blockingExecutorStarter) Start(request StartRequest) (ManagedProcess, error) {
	starter.started <- request
	<-request.Context.Done()
	return nil, request.Context.Err()
}

type runnerHarness struct {
	database  *sqlite.Database
	logs      *joblogging.Store
	service   *JobService
	runner    *Runner
	artifacts *cas.ArtifactManager
	job       job.Job
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
	contentDirectory, err := paths.ContentStoreDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	content, err := cas.New(contentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := cas.NewArtifactManager(content, database.Artifacts(), time.Now)
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
		Progress:   database.Jobs(),
		StartProcess: func(spec job.Spec, stdout, stderr io.Writer) (ManagedProcess, error) {
			return processes.Start(spec, stdout, stderr)
		},
		RunnerPID:         os.Getpid,
		Now:               time.Now,
		HeartbeatInterval: 10 * time.Millisecond,
		StopGracePeriod:   100 * time.Millisecond,
		Artifacts:         artifacts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runnerHarness{
		database: database, logs: logs, service: service, runner: runner, artifacts: artifacts, job: queued,
	}
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
