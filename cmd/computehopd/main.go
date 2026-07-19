package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/orchestrator"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
)

var version = "dev"

type options struct {
	stateDir    string
	checkOnly   bool
	showVersion bool
	runnerJob   string
}

type runtimeDependencies struct {
	disableDispatcher bool
	executable        func() (string, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "computehopd: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) (runErr error) {
	return runWithDependencies(ctx, arguments, stdout, stderr, runtimeDependencies{})
}

func runWithDependencies(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies runtimeDependencies,
) (runErr error) {
	parsed, err := parseOptions(arguments, stderr)
	if err != nil {
		return err
	}
	if parsed.showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

	stateDir := parsed.stateDir
	if stateDir == "" {
		stateDir, err = paths.StateDir()
		if err != nil {
			return fmt.Errorf("resolve state directory: %w", err)
		}
	}
	databasePath, err := paths.DatabasePath(stateDir)
	if err != nil {
		return err
	}
	if err := permissions.EnsurePrivateDirectory(stateDir); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}

	database, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("open daemon database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close daemon database: %w", err)
		}
	}()
	logStore, err := joblogging.NewStore(stateDir, database.Executions(), time.Now)
	if err != nil {
		return fmt.Errorf("initialize durable job logs: %w", err)
	}

	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs:       database.Jobs(),
		Executions: database.Executions(),
		Logs:       logStore,
		GenerateID: job.NewID,
		Now:        time.Now,
	})
	if err != nil {
		return fmt.Errorf("initialize worker job service: %w", err)
	}

	if parsed.checkOnly {
		_, err := fmt.Fprintln(stdout, "computehopd ready")
		return err
	}
	if parsed.runnerJob != "" {
		id, err := job.ParseID(parsed.runnerJob)
		if err != nil {
			return err
		}
		runner, err := worker.NewRunner(worker.RunnerDependencies{
			Jobs:       database.Jobs(),
			Executions: database.Executions(),
			Logs:       logStore,
			StartProcess: func(spec job.Spec, stdout, stderr io.Writer) (worker.NativeProcess, error) {
				return processes.Start(spec, stdout, stderr)
			},
			RunnerPID: os.Getpid,
			Now:       time.Now,
		})
		if err != nil {
			return fmt.Errorf("initialize native runner: %w", err)
		}
		return runner.Run(ctx, id)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if !localIPCSupported {
		logger.Info("computehopd started", "database", databasePath, "version", version, "local_ipc", "unsupported")
		<-ctx.Done()
		logger.Info("computehopd stopped")
		return nil
	}

	tokenPath, err := paths.CapabilityTokenPath(stateDir)
	if err != nil {
		return err
	}
	token, err := permissions.LoadOrCreateCapabilityToken(tokenPath)
	if err != nil {
		return fmt.Errorf("initialize local IPC authentication: %w", err)
	}
	socketPath, err := paths.LocalSocketPath(stateDir)
	if err != nil {
		return err
	}
	handler, err := orchestrator.NewLocalHandler(jobService, version)
	if err != nil {
		return fmt.Errorf("initialize local IPC handler: %w", err)
	}
	server, err := localipc.NewServer(socketPath, token, handler)
	if err != nil {
		return fmt.Errorf("initialize local IPC server: %w", err)
	}
	defer server.Close()

	if dependencies.executable == nil {
		dependencies.executable = os.Executable
	}
	var dispatcher *worker.Dispatcher
	if !dependencies.disableDispatcher {
		executable, err := dependencies.executable()
		if err != nil {
			return fmt.Errorf("resolve daemon executable: %w", err)
		}
		launcher, err := processes.NewRunnerLauncher(executable, stateDir)
		if err != nil {
			return fmt.Errorf("initialize runner launcher: %w", err)
		}
		dispatcher, err = worker.NewDispatcher(worker.DispatcherDependencies{
			Jobs:            database.Jobs(),
			Executions:      database.Executions(),
			Launcher:        launcher,
			ProcessAlive:    processes.Alive,
			KillProcessTree: processes.KillTree,
			Now:             time.Now,
			ReportError: func(err error) {
				logger.Error("job dispatcher reconciliation failed", "error", err)
			},
		})
		if err != nil {
			return fmt.Errorf("initialize job dispatcher: %w", err)
		}
	}

	logger.Info("computehopd started", "database", databasePath, "socket", socketPath, "version", version)
	serveContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	dispatchResult := make(chan error, 1)
	if dispatcher != nil {
		go func() {
			dispatchResult <- dispatcher.Run(serveContext)
			stopServing()
		}()
	}
	serveErr := server.Serve(serveContext)
	stopServing()
	var dispatchErr error
	if dispatcher != nil {
		dispatchErr = <-dispatchResult
	}
	if serveErr != nil {
		return fmt.Errorf("serve local IPC: %w", serveErr)
	}
	if dispatchErr != nil {
		return fmt.Errorf("dispatch jobs: %w", dispatchErr)
	}
	logger.Info("computehopd stopped")
	return nil
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	var parsed options
	flags := flag.NewFlagSet("computehopd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.stateDir, "state-dir", "", "directory for durable local state")
	flags.BoolVar(&parsed.checkOnly, "check", false, "initialize and verify local state, then exit")
	flags.BoolVar(&parsed.showVersion, "version", false, "print version and exit")
	flags.StringVar(&parsed.runnerJob, "runner-job", "", "run one internally dispatched job")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if parsed.runnerJob != "" && (parsed.checkOnly || parsed.showVersion) {
		return options{}, errors.New("--runner-job cannot be combined with --check or --version")
	}
	return parsed, nil
}
