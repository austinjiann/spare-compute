package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/orchestrator"
	pairingapp "github.com/austinjiann/spare-compute/internal/app/pairing"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/cas"
	"github.com/austinjiann/spare-compute/internal/infra/discovery/mdns"
	identityinfra "github.com/austinjiann/spare-compute/internal/infra/identity"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
	"github.com/austinjiann/spare-compute/internal/platform/resources"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/trust"
)

var version = "dev"

type options struct {
	stateDir        string
	checkOnly       bool
	showVersion     bool
	runnerJob       string
	deviceName      string
	role            string
	lanOnly         bool
	connectivityURL string
	stunServers     stringValues
	turnServers     stringValues
	turnUsername    string
	turnPassword    string
	cacheBytes      int64
}

type runtimeDependencies struct {
	disableDispatcher bool
	executable        func() (string, error)
	discovery         device.LANDiscovery
	hostname          func() (string, error)
	pairingEndpoint   func(string, session.LocalDevice, trust.Repository) (session.Endpoint, error)
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
	contentDirectory, err := paths.ContentStoreDir(stateDir)
	if err != nil {
		return err
	}
	contentStore, err := cas.NewManaged(ctx, cas.Config{
		Root: contentDirectory, Index: database.ContentCache(),
		MaximumBytes: parsed.cacheBytes, Now: time.Now,
	})
	if err != nil {
		return fmt.Errorf("initialize project content store: %w", err)
	}
	workspaceStore, err := cas.NewWorkspaceStore(stateDir, contentStore)
	if err != nil {
		return fmt.Errorf("initialize job workspaces: %w", err)
	}
	projectSnapshots, err := snapshot.NewBuilder(contentStore)
	if err != nil {
		return fmt.Errorf("initialize project snapshotter: %w", err)
	}
	artifactManager, err := cas.NewArtifactManager(contentStore, database.Artifacts(), time.Now)
	if err != nil {
		return fmt.Errorf("initialize job artifacts: %w", err)
	}

	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs:       database.Jobs(),
		Executions: database.Executions(),
		Logs:       logStore,
		GenerateID: job.NewID,
		Now:        time.Now,
		Snapshots:  workspaceStore,
		Artifacts:  artifactManager,
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
			Artifacts: artifactManager,
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
		dependencies.pairingEndpoint = func(
			address string,
			local session.LocalDevice,
			repository trust.Repository,
		) (session.Endpoint, error) {
			return quictransport.Listen(address, local, repository)
		}
	}
	pairingEndpoint, err := dependencies.pairingEndpoint(
		fmt.Sprintf(":%d", mdns.DefaultPort), localDevice, database.Trust(),
	)
	if err != nil {
		return daemonStartupError("initialize pairing endpoint", err)
	}
	defer pairingEndpoint.Close()
	if pairingEndpoint.Port() == 0 {
		return errors.New("initialize pairing endpoint: listener has no UDP port")
	}
	resourceSnapshot := resources.Static()
	localAnnouncement := device.Announcement{
		PresenceID: presenceID, Name: deviceName, Role: localRole,
		ProtocolVersion:  device.DiscoveryProtocolVersion,
		Port:             pairingEndpoint.Port(),
		EndpointReady:    true,
		Platform:         runtime.GOOS,
		Architecture:     runtime.GOARCH,
		LogicalCPUCount:  resourceSnapshot.LogicalCPUCount,
		TotalMemoryBytes: resourceSnapshot.TotalMemoryBytes,
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
	remoteHandler, err := worker.NewRemoteHandler(jobService)
	if err != nil {
		return fmt.Errorf("initialize remote worker handler: %w", err)
	}
	var remoteManager *remoteconn.Manager
	if parsed.connectivityURL != "" {
		concreteEndpoint, ok := pairingEndpoint.(*quictransport.Endpoint)
		if !ok {
			return errors.New("initialize remote connectivity: endpoint does not support selected packet paths")
		}
		client, err := rendezvous.NewClient(rendezvous.ClientConfig{BaseURL: parsed.connectivityURL})
		if err != nil {
			return fmt.Errorf("initialize rendezvous client: %w", err)
		}
		iceServers := make([]icepath.Server, 0, len(parsed.stunServers)+len(parsed.turnServers))
		for _, uri := range parsed.stunServers {
			iceServers = append(iceServers, icepath.Server{URI: uri})
		}
		for _, uri := range parsed.turnServers {
			iceServers = append(iceServers, icepath.Server{
				URI: uri, Username: parsed.turnUsername, Password: parsed.turnPassword,
			})
		}
		remoteManager, err = remoteconn.NewManager(remoteconn.Config{
			LocalRole: localRole, Trust: database.Trust(), Client: client,
			ICE: icepath.Config{Servers: iceServers},
			NewPath: func(connection net.PacketConn, remote net.Addr) (remoteconn.Path, error) {
				return concreteEndpoint.NewRemotePath(connection, remote)
			},
			Now: time.Now,
			ReportError: func(id device.ID, err error) {
				logger.Warn("remote connectivity unavailable", "device_id", id.Short(), "error", err)
			},
		})
		if err != nil {
			return fmt.Errorf("initialize remote connectivity: %w", err)
		}
	}
	remoteJobs, err := orchestrator.NewRemoteJobService(orchestrator.RemoteDependencies{
		Nearby: deviceService, Trust: database.Trust(),
		Placements: database.Placements(), Progress: database.Jobs(),
		Dialer: pairingEndpoint, Remote: remoteManager,
		Snapshots: projectSnapshots, Content: contentStore,
		ArtifactContent: contentStore, Artifacts: artifactManager,
	})
	if err != nil {
		return fmt.Errorf("initialize remote job service: %w", err)
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
	localDeviceInfo := orchestrator.LocalDeviceInfo{
		DeviceID:         localDevice.Identity.ID(),
		Name:             localDevice.Name,
		Role:             localDevice.Role,
		Platform:         runtime.GOOS,
		Architecture:     runtime.GOARCH,
		LogicalCPUCount:  resourceSnapshot.LogicalCPUCount,
		TotalMemoryBytes: resourceSnapshot.TotalMemoryBytes,
	}
	var connectivityControllers []orchestrator.ConnectivityController
	if remoteManager != nil {
		connectivityControllers = append(connectivityControllers, remoteManager)
	}
	handler, err := orchestrator.NewLocalHandlerWithLocalDevice(
		jobService, remoteJobs, deviceService, pairingService, localDeviceInfo, version, connectivityControllers...,
	)
	if err != nil {
		return fmt.Errorf("initialize local IPC handler: %w", err)
	}
	server, err := localipc.NewServer(socketPath, token, handler)
	if err != nil {
		return daemonStartupError("initialize local IPC server", err)
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
		launcher, err := processes.NewRunnerLauncher(executable, stateDir, parsed.cacheBytes)
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
		"lan_only", parsed.lanOnly,
		"remote_connectivity", remoteManager != nil,
		"cache_limit_bytes", parsed.cacheBytes,
		"version", version,
	)
	serveContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	backgroundResult := make(chan error, 4)
	backgroundCount := 2
	go func() {
		backgroundResult <- deviceService.Run(serveContext)
		stopServing()
	}()
	go func() {
		backgroundResult <- pairingEndpoint.Run(
			serveContext,
			func(channel session.PairingChannel) { pairingService.Accept(serveContext, channel) },
			remoteHandler,
		)
		stopServing()
	}()
	if dispatcher != nil {
		backgroundCount++
		go func() {
			backgroundResult <- dispatcher.Run(serveContext)
			stopServing()
		}()
	}
	if remoteManager != nil {
		backgroundCount++
		go func() {
			backgroundResult <- remoteManager.Run(serveContext, remoteHandler)
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

func daemonStartupError(stage string, err error) error {
	if errors.Is(err, localipc.ErrDaemonAlreadyRunning) || addressAlreadyInUse(err) {
		return fmt.Errorf(
			"%s: another ComputeHop daemon already appears to be running; run 'computehop status' to use it, or stop the existing terminal or launch agent before starting another daemon: %w",
			stage,
			err,
		)
	}
	return fmt.Errorf("%s: %w", stage, err)
}

func addressAlreadyInUse(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "address already in use")
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	parsed := options{cacheBytes: contentcache.DefaultMaximumBytes}
	flags := flag.NewFlagSet("computehopd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.stateDir, "state-dir", "", "directory for durable local state")
	flags.BoolVar(&parsed.checkOnly, "check", false, "initialize and verify local state, then exit")
	flags.BoolVar(&parsed.showVersion, "version", false, "print version and exit")
	flags.StringVar(&parsed.runnerJob, "runner-job", "", "run one internally dispatched job")
	flags.StringVar(&parsed.deviceName, "device-name", "", "human-readable LAN device name")
	flags.StringVar(&parsed.role, "role", "", "device role: orchestrator or worker")
	flags.BoolVar(
		&parsed.lanOnly,
		"lan-only",
		false,
		"disable hosted rendezvous, ICE, and TURN; use same-LAN discovery only",
	)
	flags.StringVar(
		&parsed.connectivityURL,
		"connectivity-url",
		"",
		"HTTPS URL of the optional ComputeHop rendezvous service",
	)
	flags.Var(
		&parsed.stunServers,
		"stun-server",
		"STUN URI used for direct internet paths; may be repeated",
	)
	flags.Var(
		&parsed.turnServers,
		"turn-server",
		"TURN URI used for relay fallback; may be repeated",
	)
	flags.StringVar(&parsed.turnUsername, "turn-username", "", "TURN username for all configured TURN servers")
	flags.StringVar(&parsed.turnPassword, "turn-password", "", "TURN password for all configured TURN servers")
	flags.Var(
		&byteSizeValue{target: &parsed.cacheBytes},
		"cache-size",
		"maximum verified content cache size (for example 20GiB or 512MB)",
	)
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if parsed.runnerJob != "" && (parsed.checkOnly || parsed.showVersion) {
		return options{}, errors.New("--runner-job cannot be combined with --check or --version")
	}
	serverCount := len(parsed.stunServers) + len(parsed.turnServers)
	if parsed.lanOnly && (parsed.connectivityURL != "" || serverCount > 0 || parsed.turnUsername != "" || parsed.turnPassword != "") {
		return options{}, errors.New("--lan-only cannot be combined with remote connectivity flags")
	}
	if (parsed.connectivityURL == "") != (serverCount == 0) {
		return options{}, errors.New("--connectivity-url and at least one --stun-server or --turn-server must be supplied together")
	}
	if len(parsed.turnServers) > 0 && (parsed.turnUsername == "" || parsed.turnPassword == "") {
		return options{}, errors.New("--turn-server requires --turn-username and --turn-password")
	}
	if len(parsed.turnServers) == 0 && (parsed.turnUsername != "" || parsed.turnPassword != "") {
		return options{}, errors.New("--turn-username and --turn-password require --turn-server")
	}
	if err := contentcache.ValidateMaximumBytes(parsed.cacheBytes); err != nil {
		return options{}, fmt.Errorf("--cache-size: %w", err)
	}
	return parsed, nil
}

type stringValues []string

func (values *stringValues) String() string {
	return strings.Join(*values, ",")
}

func (values *stringValues) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("server URI must not be empty")
	}
	*values = append(*values, value)
	return nil
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
