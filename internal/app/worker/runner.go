package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
)

const (
	defaultHeartbeatInterval = time.Second
	defaultStopGracePeriod   = 5 * time.Second
)

// NativeProcess is the process-tree boundary required by a runner.
type NativeProcess interface {
	PID() int
	GracefulStop() error
	Kill() error
	Wait() processes.Exit
}

// ProcessStarter starts a native process with separately tagged output streams.
type ProcessStarter func(job.Spec, io.Writer, io.Writer) (NativeProcess, error)

// LogStore is the durable output boundary required by a runner.
type LogStore interface {
	OpenWriter(context.Context, job.ID) (*joblogging.Writer, error)
	Read(context.Context, job.ID, uint64, int) (joblogging.Page, error)
}

// RunnerDependencies are explicit to keep process supervision deterministic in tests.
type RunnerDependencies struct {
	Jobs              job.Repository
	Executions        execution.Repository
	Logs              LogStore
	StartProcess      ProcessStarter
	RunnerPID         func() int
	Now               func() time.Time
	HeartbeatInterval time.Duration
	StopGracePeriod   time.Duration
	Artifacts         ArtifactCollector
}

// ArtifactCollector snapshots and durably publishes declared job outputs.
type ArtifactCollector interface {
	Collect(context.Context, job.Job) (artifact.Bundle, error)
}

// Runner owns one claimed native process until a durable terminal result exists.
type Runner struct {
	jobs              job.Repository
	executions        execution.Repository
	logs              LogStore
	startProcess      ProcessStarter
	runnerPID         func() int
	now               func() time.Time
	heartbeatInterval time.Duration
	stopGracePeriod   time.Duration
	artifacts         ArtifactCollector
}

// NewRunner validates and constructs a supervised native runner.
func NewRunner(dependencies RunnerDependencies) (*Runner, error) {
	if dependencies.Jobs == nil || dependencies.Executions == nil || dependencies.Logs == nil ||
		dependencies.StartProcess == nil || dependencies.RunnerPID == nil || dependencies.Now == nil {
		return nil, ErrMissingDependency
	}
	if dependencies.HeartbeatInterval == 0 {
		dependencies.HeartbeatInterval = defaultHeartbeatInterval
	}
	if dependencies.StopGracePeriod == 0 {
		dependencies.StopGracePeriod = defaultStopGracePeriod
	}
	if dependencies.HeartbeatInterval < 0 || dependencies.StopGracePeriod < 0 {
		return nil, fmt.Errorf("%w: runner intervals must not be negative", ErrMissingDependency)
	}
	return &Runner{
		jobs:              dependencies.Jobs,
		executions:        dependencies.Executions,
		logs:              dependencies.Logs,
		startProcess:      dependencies.StartProcess,
		runnerPID:         dependencies.RunnerPID,
		now:               dependencies.Now,
		heartbeatInterval: dependencies.HeartbeatInterval,
		stopGracePeriod:   dependencies.StopGracePeriod,
		artifacts:         dependencies.Artifacts,
	}, nil
}

// Run claims id and retains custody until the process tree and logs are complete.
func (runner *Runner) Run(ctx context.Context, id job.ID) error {
	pid := runner.runnerPID()
	if _, err := runner.executions.Claim(ctx, id, pid, runner.now()); err != nil {
		return fmt.Errorf("claim job: %w", err)
	}
	current, err := runner.jobs.Get(ctx, id)
	if err != nil {
		return runner.failClaimed(ctx, id, pid, "load_job", "load claimed job", err)
	}
	writer, err := runner.logs.OpenWriter(context.Background(), id)
	if err != nil {
		return runner.failClaimed(ctx, id, pid, "open_logs", "open durable job logs", err)
	}

	completion, runErr := runner.runClaimed(ctx, current, pid, writer)
	if completion.Kind() == execution.CompletionSucceeded && len(current.Spec.Outputs) > 0 {
		completion, runErr = runner.collectDeclaredArtifacts(ctx, current.ID, pid)
	}
	closeErr := writer.Close()
	if closeErr != nil && completion.Failure == nil && !completion.Cancelled {
		completion = failedCompletion(runner.now(), "close_logs", "close durable job logs", closeErr, completion.ExitCode, completion.TerminationSignal)
	}
	if completion.At.IsZero() {
		completion.At = runner.now().UTC()
	}
	if _, _, err := runner.executions.Complete(context.Background(), id, pid, completion); err != nil {
		return errors.Join(runErr, closeErr, fmt.Errorf("persist job completion: %w", err))
	}
	return errors.Join(runErr, closeErr)
}

func (runner *Runner) collectDeclaredArtifacts(
	ctx context.Context,
	id job.ID,
	runnerPID int,
) (execution.Completion, error) {
	if runner.artifacts == nil {
		return failedCompletion(
			runner.now(), "artifacts_unavailable", "collect declared outputs",
			errors.New("artifact collection is not configured"), nil, "",
		), nil
	}
	current, err := runner.jobs.Get(ctx, id)
	if err != nil {
		return failedCompletion(runner.now(), "load_job", "load job before artifact collection", err, nil, ""), err
	}
	_, transition, err := current.Apply(job.StateCollecting, runner.now(), nil)
	if err != nil {
		return failedCompletion(runner.now(), "begin_collection", "begin artifact collection", err, nil, ""), err
	}
	collecting, err := runner.jobs.ApplyTransition(ctx, transition)
	if err != nil {
		return failedCompletion(runner.now(), "begin_collection", "persist artifact collection state", err, nil, ""), err
	}
	collectContext, cancel := context.WithCancel(ctx)
	defer cancel()
	collected := make(chan error, 1)
	go func() {
		_, collectErr := runner.artifacts.Collect(collectContext, collecting)
		collected <- collectErr
	}()
	ticker := time.NewTicker(runner.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case collectErr := <-collected:
			if collectErr != nil {
				return failedCompletion(
					runner.now(), "collect_artifacts", "collect declared outputs", collectErr, nil, "",
				), nil
			}
			return execution.Completion{At: runner.now().UTC(), ExitCode: intPointer(0)}, nil
		case <-ticker.C:
			if err := runner.executions.Heartbeat(ctx, id, runnerPID, runner.now()); err != nil {
				cancel()
				<-collected
				return failedCompletion(runner.now(), "heartbeat", "persist artifact collection heartbeat", err, nil, ""), err
			}
			requested, err := runner.executions.CancellationRequested(ctx, id, runnerPID)
			if err != nil {
				cancel()
				<-collected
				return failedCompletion(runner.now(), "check_cancellation", "poll artifact cancellation", err, nil, ""), err
			}
			if requested {
				cancel()
				<-collected
				return execution.Completion{At: runner.now().UTC(), Cancelled: true}, nil
			}
		case <-ctx.Done():
			cancel()
			<-collected
			return failedCompletion(runner.now(), "runner_stopped", "runner stopped during artifact collection", ctx.Err(), nil, ""), ctx.Err()
		}
	}
}

func (runner *Runner) runClaimed(
	ctx context.Context,
	current job.Job,
	runnerPID int,
	writer *joblogging.Writer,
) (execution.Completion, error) {
	cancelled, err := runner.executions.CancellationRequested(ctx, current.ID, runnerPID)
	if err != nil {
		return failedCompletion(runner.now(), "check_cancellation", "check cancellation before start", err, nil, ""), err
	}
	if cancelled {
		return execution.Completion{At: runner.now().UTC(), Cancelled: true}, nil
	}

	process, err := runner.startProcess(
		current.Spec,
		writer.Stream(context.Background(), joblogging.StreamStdout),
		writer.Stream(context.Background(), joblogging.StreamStderr),
	)
	if err != nil {
		return failedCompletion(runner.now(), "process_start", "start native process", err, nil, ""), nil
	}
	if _, err := runner.executions.MarkRunning(ctx, current.ID, runnerPID, process.PID(), runner.now()); err != nil {
		_ = process.Kill()
		exit := process.Wait()
		return failedCompletion(runner.now(), "mark_running", "record running process", err, intPointer(exit.Code), exit.Signal), err
	}

	waited := make(chan processes.Exit, 1)
	go func() { waited <- process.Wait() }()
	ticker := time.NewTicker(runner.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case exit := <-waited:
			return completionForExit(runner.now(), exit), exit.WaitErr
		case <-ticker.C:
			if err := runner.executions.Heartbeat(ctx, current.ID, runnerPID, runner.now()); err != nil {
				return runner.stopAfterFailure(process, waited, "heartbeat", "persist runner heartbeat", err)
			}
			requested, err := runner.executions.CancellationRequested(ctx, current.ID, runnerPID)
			if err != nil {
				return runner.stopAfterFailure(process, waited, "check_cancellation", "poll cancellation request", err)
			}
			if requested {
				exit := runner.stop(process, waited)
				return execution.Completion{
					At:                runner.now().UTC(),
					ExitCode:          intPointer(exit.Code),
					TerminationSignal: exit.Signal,
					Cancelled:         true,
				}, exit.WaitErr
			}
		case <-ctx.Done():
			exit := runner.stop(process, waited)
			return failedCompletion(runner.now(), "runner_stopped", "runner was stopped", ctx.Err(), intPointer(exit.Code), exit.Signal), ctx.Err()
		}
	}
}

func (runner *Runner) stopAfterFailure(
	process NativeProcess,
	waited <-chan processes.Exit,
	code string,
	message string,
	cause error,
) (execution.Completion, error) {
	exit := runner.stop(process, waited)
	return failedCompletion(runner.now(), code, message, cause, intPointer(exit.Code), exit.Signal), errors.Join(cause, exit.WaitErr)
}

func (runner *Runner) stop(process NativeProcess, waited <-chan processes.Exit) processes.Exit {
	_ = process.GracefulStop()
	timer := time.NewTimer(runner.stopGracePeriod)
	defer timer.Stop()
	select {
	case exit := <-waited:
		return exit
	case <-timer.C:
		_ = process.Kill()
		return <-waited
	}
}

func (runner *Runner) failClaimed(
	ctx context.Context,
	id job.ID,
	runnerPID int,
	code string,
	message string,
	cause error,
) error {
	completion := failedCompletion(runner.now(), code, message, cause, nil, "")
	_, _, completeErr := runner.executions.Complete(context.Background(), id, runnerPID, completion)
	return errors.Join(cause, completeErr, ctx.Err())
}

func completionForExit(at time.Time, exit processes.Exit) execution.Completion {
	exitCode := intPointer(exit.Code)
	if exit.Code == 0 && exit.Signal == "" && exit.WaitErr == nil {
		return execution.Completion{At: at.UTC(), ExitCode: exitCode}
	}
	message := fmt.Sprintf("process exited with code %d", exit.Code)
	if exit.Signal != "" {
		message = fmt.Sprintf("process terminated by %s", exit.Signal)
	}
	if exit.WaitErr != nil {
		message = exit.WaitErr.Error()
	}
	return execution.Completion{
		At:                at.UTC(),
		ExitCode:          exitCode,
		TerminationSignal: exit.Signal,
		Failure:           &job.Failure{Code: "process_exit", Message: message},
	}
}

func failedCompletion(
	at time.Time,
	code string,
	message string,
	cause error,
	exitCode *int,
	signal string,
) execution.Completion {
	if cause != nil {
		message += ": " + cause.Error()
	}
	return execution.Completion{
		At:                at.UTC(),
		ExitCode:          exitCode,
		TerminationSignal: signal,
		Failure:           &job.Failure{Code: code, Message: message},
	}
}

func intPointer(value int) *int {
	return &value
}
