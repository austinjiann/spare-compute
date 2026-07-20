package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/execution"
	"github.com/austinjiann/spare-compute/internal/job"
)

const (
	defaultDispatchInterval = 250 * time.Millisecond
	defaultLaunchRetry      = 5 * time.Second
	defaultRunnerStaleAfter = 10 * time.Second
)

// RunnerLauncher starts one independently supervised runner for a queued job.
type RunnerLauncher interface {
	Launch(job.ID) error
}

// DispatcherDependencies configure durable queue polling and runner recovery.
type DispatcherDependencies struct {
	Jobs             job.Repository
	Executions       execution.Repository
	Launcher         RunnerLauncher
	ProcessAlive     func(int) bool
	KillProcessTree  func(int) error
	Now              func() time.Time
	ReportError      func(error)
	DispatchInterval time.Duration
	LaunchRetry      time.Duration
	RunnerStaleAfter time.Duration
}

// Dispatcher launches queued jobs and fails attempts whose owning runner died.
type Dispatcher struct {
	jobs             job.Repository
	executions       execution.Repository
	launcher         RunnerLauncher
	processAlive     func(int) bool
	killProcessTree  func(int) error
	now              func() time.Time
	reportError      func(error)
	dispatchInterval time.Duration
	launchRetry      time.Duration
	runnerStaleAfter time.Duration
	launched         map[job.ID]time.Time
}

// NewDispatcher validates and constructs a local durable dispatcher.
func NewDispatcher(dependencies DispatcherDependencies) (*Dispatcher, error) {
	if dependencies.Jobs == nil || dependencies.Executions == nil || dependencies.Launcher == nil ||
		dependencies.ProcessAlive == nil || dependencies.KillProcessTree == nil || dependencies.Now == nil {
		return nil, ErrMissingDependency
	}
	if dependencies.DispatchInterval == 0 {
		dependencies.DispatchInterval = defaultDispatchInterval
	}
	if dependencies.ReportError == nil {
		dependencies.ReportError = func(error) {}
	}
	if dependencies.LaunchRetry == 0 {
		dependencies.LaunchRetry = defaultLaunchRetry
	}
	if dependencies.RunnerStaleAfter == 0 {
		dependencies.RunnerStaleAfter = defaultRunnerStaleAfter
	}
	if dependencies.DispatchInterval < 0 || dependencies.LaunchRetry < 0 || dependencies.RunnerStaleAfter < 0 {
		return nil, fmt.Errorf("%w: dispatcher intervals must not be negative", ErrMissingDependency)
	}
	return &Dispatcher{
		jobs:             dependencies.Jobs,
		executions:       dependencies.Executions,
		launcher:         dependencies.Launcher,
		processAlive:     dependencies.ProcessAlive,
		killProcessTree:  dependencies.KillProcessTree,
		now:              dependencies.Now,
		reportError:      dependencies.ReportError,
		dispatchInterval: dependencies.DispatchInterval,
		launchRetry:      dependencies.LaunchRetry,
		runnerStaleAfter: dependencies.RunnerStaleAfter,
		launched:         make(map[job.ID]time.Time),
	}, nil
}

// Run continuously reconciles durable queue state until ctx ends.
func (dispatcher *Dispatcher) Run(ctx context.Context) error {
	if err := dispatcher.reconcile(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		dispatcher.reportError(err)
	}
	ticker := time.NewTicker(dispatcher.dispatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := dispatcher.reconcile(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				dispatcher.reportError(err)
			}
		}
	}
}

func (dispatcher *Dispatcher) reconcile(ctx context.Context) error {
	now := dispatcher.now().UTC()
	if err := dispatcher.recoverLostRunners(ctx, now); err != nil {
		return err
	}
	queued, err := dispatcher.jobs.List(ctx, job.ListOptions{States: []job.State{job.StateQueued}, Limit: 500})
	if err != nil {
		return fmt.Errorf("list queued jobs: %w", err)
	}
	present := make(map[job.ID]struct{}, len(queued))
	for _, current := range queued {
		present[current.ID] = struct{}{}
		lastLaunch, alreadyLaunched := dispatcher.launched[current.ID]
		if alreadyLaunched && now.Sub(lastLaunch) < dispatcher.launchRetry {
			continue
		}
		dispatcher.launched[current.ID] = now
		if err := dispatcher.launcher.Launch(current.ID); err != nil {
			return err
		}
	}
	for id := range dispatcher.launched {
		if _, exists := present[id]; !exists {
			delete(dispatcher.launched, id)
		}
	}
	return nil
}

func (dispatcher *Dispatcher) recoverLostRunners(ctx context.Context, now time.Time) error {
	attempts, err := dispatcher.executions.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("list active executions: %w", err)
	}
	for _, attempt := range attempts {
		if dispatcher.processAlive(attempt.RunnerPID) || now.Sub(attempt.HeartbeatAt) < dispatcher.runnerStaleAfter {
			continue
		}
		if attempt.ProcessPID > 0 {
			if err := dispatcher.killProcessTree(attempt.ProcessPID); err != nil {
				return fmt.Errorf("stop orphaned process tree for job %s: %w", attempt.JobID, err)
			}
		}
		failure := &job.Failure{
			Code:      "runner_lost",
			Message:   "the process supervising this job exited unexpectedly",
			Retryable: true,
		}
		_, _, err := dispatcher.executions.Complete(ctx, attempt.JobID, attempt.RunnerPID, execution.Completion{
			At:      now,
			Failure: failure,
		})
		if err != nil && !errors.Is(err, execution.ErrAttemptCompleted) {
			return fmt.Errorf("recover job %s: %w", attempt.JobID, err)
		}
	}
	return nil
}
