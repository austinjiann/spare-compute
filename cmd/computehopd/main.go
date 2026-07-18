package main

import (
	"context"
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
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
)

var version = "dev"

type options struct {
	stateDir    string
	checkOnly   bool
	showVersion bool
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

	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs:       database.Jobs(),
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

	logger.Info("computehopd started", "database", databasePath, "socket", socketPath, "version", version)
	if err := server.Serve(ctx); err != nil {
		return fmt.Errorf("serve local IPC: %w", err)
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
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return parsed, nil
}
