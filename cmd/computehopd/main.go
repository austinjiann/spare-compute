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

	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
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

	database, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("open daemon database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close daemon database: %w", err)
		}
	}()

	_, err = worker.NewJobService(worker.Dependencies{
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
	logger.Info("computehopd started", "database", databasePath, "version", version)
	<-ctx.Done()
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
