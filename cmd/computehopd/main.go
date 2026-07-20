package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/orchestrator"
	pairingapp "github.com/austinjiann/spare-compute/internal/app/pairing"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/discovery/mdns"
	identityinfra "github.com/austinjiann/spare-compute/internal/infra/identity"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
	"github.com/austinjiann/spare-compute/internal/session"
)

var version = "dev"

type options struct {
	stateDir    string
	checkOnly   bool
	showVersion bool
	runnerJob   string
	deviceName  string
	role        string
}

type runtimeDependencies struct {
	disableDispatcher bool
	executable        func() (string, error)
	discovery         device.LANDiscovery
	hostname          func() (string, error)
	pairingEndpoint   func(string, session.LocalDevice) (session.PairingEndpoint, error)
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
	identityPath, err := paths.DeviceIdentityPath(stateDir)
	if err != nil {
		return err
	}
	identityStore, err := identityinfra.NewStore(identityPath)
	if err != nil {
		return fmt.Errorf("initialize device identity store: %w", err)
	}
	localIdentity, err := identityStore.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}
	if dependencies.hostname == nil {
		dependencies.hostname = os.Hostname
	}
	deviceName := parsed.deviceName
	if deviceName == "" {
		deviceName, err = dependencies.hostname()
		if err != nil {
			return fmt.Errorf("resolve device name: %w", err)
		}
	}
	presenceID, err := device.NewPresenceID(rand.Reader)
	if err != nil {
		return err
	}
	localRole, err := configuredRole(parsed.role)
	if err != nil {
		return err
	}
	localDevice := session.LocalDevice{
		Identity: localIdentity, Name: deviceName, Role: localRole, PresenceID: presenceID,
	}
	if err := localDevice.Validate(); err != nil {
		return fmt.Errorf("configure local device: %w", err)
	}
	if err := pairingapp.ValidateLocalRole(ctx, localRole, database.Trust()); err != nil {
		return fmt.Errorf("validate device role: %w", err)
	}
	if parsed.checkOnly {
		_, err := fmt.Fprintln(stdout, "computehopd ready")
		return err
	}

	if dependencies.pairingEndpoint == nil {
		dependencies.pairingEndpoint = func(address string, local session.LocalDevice) (session.PairingEndpoint, error) {
			return quictransport.Listen(address, local)
		}
	}
	pairingEndpoint, err := dependencies.pairingEndpoint(
		fmt.Sprintf(":%d", mdns.DefaultPort), localDevice,
	)
	if err != nil {
		return fmt.Errorf("initialize pairing endpoint: %w", err)
	}
	defer pairingEndpoint.Close()
	if pairingEndpoint.Port() == 0 {
		return errors.New("initialize pairing endpoint: listener has no UDP port")
	}
	localAnnouncement := device.Announcement{
		PresenceID: presenceID, Name: deviceName, Role: localRole,
		ProtocolVersion: device.DiscoveryProtocolVersion,
		Port:            pairingEndpoint.Port(),
		EndpointReady:   true,
	}
	if err := localAnnouncement.Validate(); err != nil {
		return fmt.Errorf("configure local device announcement: %w", err)
	}
	if dependencies.discovery == nil {
		dependencies.discovery = mdns.New()
	}
	deviceService, err := orchestrator.NewDeviceService(orchestrator.DeviceDependencies{
		Local:       localAnnouncement,
		Discovery:   dependencies.discovery,
		Now:         time.Now,
		ReportError: func(err error) { logger.Warn("LAN discovery unavailable", "error", err) },
	})
	if err != nil {
		return fmt.Errorf("initialize LAN discovery: %w", err)
	}
	pairingService, err := pairingapp.NewService(pairingapp.Dependencies{
		Local: localDevice, Nearby: deviceService, Trust: database.Trust(),
		Endpoint: pairingEndpoint, Now: time.Now,
		ReportError: func(err error) { logger.Warn("device pairing failed", "error", err) },
	})
	if err != nil {
		return fmt.Errorf("initialize pairing service: %w", err)
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
	handler, err := orchestrator.NewLocalHandler(jobService, deviceService, pairingService, version)
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

	logger.Info(
		"computehopd started",
		"database", databasePath,
		"socket", socketPath,
		"device", deviceName,
		"device_id", localIdentity.ID().Short(),
		"role", localRole,
		"version", version,
	)
	serveContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	backgroundResult := make(chan error, 3)
	backgroundCount := 2
	go func() {
		backgroundResult <- deviceService.Run(serveContext)
		stopServing()
	}()
	go func() {
		backgroundResult <- pairingService.Run(serveContext)
		stopServing()
	}()
	if dispatcher != nil {
		backgroundCount++
		go func() {
			backgroundResult <- dispatcher.Run(serveContext)
			stopServing()
		}()
	}
	serveErr := server.Serve(serveContext)
	stopServing()
	var backgroundErr error
	for range backgroundCount {
		backgroundErr = errors.Join(backgroundErr, <-backgroundResult)
	}
	if serveErr != nil {
		return fmt.Errorf("serve local IPC: %w", serveErr)
	}
	if backgroundErr != nil {
		return fmt.Errorf("run daemon background services: %w", backgroundErr)
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
	flags.StringVar(&parsed.deviceName, "device-name", "", "human-readable LAN device name")
	flags.StringVar(&parsed.role, "role", "", "device role: orchestrator or worker")
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

func configuredRole(value string) (device.Role, error) {
	if value == "" {
		if runtime.GOOS == "darwin" {
			return device.RoleOrchestrator, nil
		}
		return device.RoleWorker, nil
	}
	role := device.Role(value)
	if role != device.RoleOrchestrator && role != device.RoleWorker {
		return "", fmt.Errorf("invalid device role %q: use orchestrator or worker", value)
	}
	if role == device.RoleOrchestrator && runtime.GOOS != "darwin" {
		return "", errors.New("orchestrator role is supported only on macOS")
	}
	return role, nil
}
