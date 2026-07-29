package orchestrator

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/cas"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestRemoteJobRoundTripFallsBackToSupervisedICEPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workerDatabase := openRemoteIntegrationDatabase(t, "remote-worker")
	orchestratorDatabase := openRemoteIntegrationDatabase(t, "remote-orchestrator")
	workerState := t.TempDir()
	logs, err := joblogging.NewStore(workerState, workerDatabase.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	workerContentDirectory, _ := paths.ContentStoreDir(workerState)
	workerContent, err := cas.New(workerContentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := cas.NewWorkspaceStore(workerState, workerContent)
	if err != nil {
		t.Fatal(err)
	}
	countedWorkspace := &countingSnapshotWorkspace{WorkspaceStore: materializer}
	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs: workerDatabase.Jobs(), Executions: workerDatabase.Executions(), Logs: logs,
		GenerateID: job.NewID, Now: time.Now, Snapshots: countedWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteHandler, err := worker.NewRemoteHandler(jobService)
	if err != nil {
		t.Fatal(err)
	}

	workerLocal := remoteIntegrationDevice(t, "Internet Worker", device.RoleWorker)
	orchestratorLocal := remoteIntegrationDevice(t, "Internet Mac", device.RoleOrchestrator)
	pairID, err := trust.NewPairID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make(trust.ConnectivitySecret, trust.ConnectivitySecretBytes)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	pairedAt := time.Now().UTC()
	workerPeer := remoteIntegrationPeer(pairID, workerLocal, pairedAt)
	workerPeer.ConnectivitySecret = append(trust.ConnectivitySecret(nil), secret...)
	orchestratorPeer := remoteIntegrationPeer(pairID, orchestratorLocal, pairedAt)
	orchestratorPeer.ConnectivitySecret = append(trust.ConnectivitySecret(nil), secret...)
	if err := orchestratorDatabase.Trust().Activate(ctx, workerPeer); err != nil {
		t.Fatal(err)
	}
	if err := workerDatabase.Trust().Activate(ctx, orchestratorPeer); err != nil {
		t.Fatal(err)
	}

	service, err := rendezvous.New(rendezvous.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workerEndpoint, err := quictransport.Listen("127.0.0.1:0", workerLocal, workerDatabase.Trust())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := quictransport.Listen(
		"127.0.0.1:0", orchestratorLocal, orchestratorDatabase.Trust(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })

	managerConfig := func(
		role device.Role,
		repository trust.Repository,
		endpoint *quictransport.Endpoint,
	) remoteconn.Config {
		return remoteconn.Config{
			LocalRole: role, Trust: repository, Client: client,
			NewPath: func(connection net.PacketConn, remote net.Addr) (remoteconn.Path, error) {
				return endpoint.NewRemotePath(connection, remote)
			},
			TrustRefreshInterval: 100 * time.Millisecond,
			PublishInterval:      100 * time.Millisecond,
			RetryDelay:           100 * time.Millisecond,
			GatherTimeout:        5 * time.Second,
			ExchangeTimeout:      5 * time.Second,
			ConnectTimeout:       5 * time.Second,
		}
	}
	workerManager, err := remoteconn.NewManager(managerConfig(
		device.RoleWorker, workerDatabase.Trust(), workerEndpoint,
	))
	if err != nil {
		t.Fatal(err)
	}
	orchestratorManager, err := remoteconn.NewManager(managerConfig(
		device.RoleOrchestrator, orchestratorDatabase.Trust(), orchestratorEndpoint,
	))
	if err != nil {
		t.Fatal(err)
	}
	managerContext, stopManagers := context.WithCancel(ctx)
	managerResults := make(chan error, 2)
	go func() { managerResults <- workerManager.Run(managerContext, remoteHandler) }()
	go func() { managerResults <- orchestratorManager.Run(managerContext, nil) }()
	t.Cleanup(func() {
		stopManagers()
		for range 2 {
			select {
			case err := <-managerResults:
				if err != nil {
					t.Errorf("manager shutdown: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("manager did not stop")
			}
		}
	})

	orchestratorState := t.TempDir()
	orchestratorContentDirectory, _ := paths.ContentStoreDir(orchestratorState)
	orchestratorContent, err := cas.New(orchestratorContentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := snapshot.NewBuilder(orchestratorContent)
	if err != nil {
		t.Fatal(err)
	}
	remoteJobs, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true}, nil
		}},
		Trust: orchestratorDatabase.Trust(), Placements: orchestratorDatabase.Placements(),
		Dialer: orchestratorEndpoint, Remote: orchestratorManager,
		Snapshots: builder, Content: orchestratorContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.test/ice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "input.txt"), []byte("over ICE"), 0o644); err != nil {
		t.Fatal(err)
	}
	submitted, err := remoteJobs.Submit(ctx, "Internet Worker", job.Spec{
		Executable: "echo", Arguments: []string{"over ICE"}, WorkingDirectory: project,
		Executor: job.ExecutorNative,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if submitted.State != job.StateQueued || countedWorkspace.puts.Load() == 0 ||
		!pathInside(submitted.Spec.WorkingDirectory, workerState) {
		t.Fatalf("submitted = %#v, uploaded = %d", submitted, countedWorkspace.puts.Load())
	}
	listed, err := remoteJobs.List(ctx, "Internet Worker", job.ListOptions{Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].ID != submitted.ID {
		t.Fatalf("List() = %#v, %v", listed, err)
	}
	cancelled, err := remoteJobs.Cancel(ctx, "", submitted.ID)
	if err != nil || cancelled.State != job.StateCancelled {
		t.Fatalf("Cancel() = %#v, %v", cancelled, err)
	}
}

func TestSnapshotTransferExecutesInIsolatedWorkspaceAndReusesWorkerCache(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workerState := t.TempDir()
	orchestratorState := t.TempDir()
	workerDatabase, err := sqlite.Open(ctx, filepath.Join(workerState, "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerDatabase.Close() })
	orchestratorDatabase, err := sqlite.Open(ctx, filepath.Join(orchestratorState, "orchestrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorDatabase.Close() })
	logs, err := joblogging.NewStore(workerState, workerDatabase.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	workerContentDirectory, _ := paths.ContentStoreDir(workerState)
	workerContent, err := cas.New(workerContentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := cas.NewWorkspaceStore(workerState, workerContent)
	if err != nil {
		t.Fatal(err)
	}
	countedWorkspace := &countingSnapshotWorkspace{WorkspaceStore: materializer}
	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs: workerDatabase.Jobs(), Executions: workerDatabase.Executions(), Logs: logs,
		GenerateID: job.NewID, Now: time.Now, Snapshots: countedWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteHandler, err := worker.NewRemoteHandler(jobService)
	if err != nil {
		t.Fatal(err)
	}

	workerLocal := remoteIntegrationDevice(t, "Snapshot Worker", device.RoleWorker)
	orchestratorLocal := remoteIntegrationDevice(t, "Snapshot Mac", device.RoleOrchestrator)
	pairID, err := trust.NewPairID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pairedAt := time.Now().UTC()
	if err := orchestratorDatabase.Trust().Activate(ctx, remoteIntegrationPeer(pairID, workerLocal, pairedAt)); err != nil {
		t.Fatal(err)
	}
	if err := workerDatabase.Trust().Activate(ctx, remoteIntegrationPeer(pairID, orchestratorLocal, pairedAt)); err != nil {
		t.Fatal(err)
	}
	workerEndpoint, err := quictransport.Listen("127.0.0.1:0", workerLocal, workerDatabase.Trust())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := quictransport.Listen(
		"127.0.0.1:0", orchestratorLocal, orchestratorDatabase.Trust(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })
	serveContext, stopServing := context.WithCancel(ctx)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- workerEndpoint.Run(
			serveContext, func(channel session.PairingChannel) { _ = channel.Close() }, remoteHandler,
		)
	}()
	t.Cleanup(func() {
		stopServing()
		select {
		case err := <-serveResult:
			if err != nil {
				t.Errorf("worker endpoint shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker endpoint did not stop")
		}
	})

	orchestratorContentDirectory, _ := paths.ContentStoreDir(orchestratorState)
	orchestratorContent, err := cas.New(orchestratorContentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := snapshot.NewBuilder(orchestratorContent)
	if err != nil {
		t.Fatal(err)
	}
	nearby := remoteIntegrationNearby(t, workerLocal, workerEndpoint.Port())
	remoteJobs, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{nearby}}, nil
		}},
		Trust: orchestratorDatabase.Trust(), Placements: orchestratorDatabase.Placements(),
		Dialer: orchestratorEndpoint, Snapshots: builder, Content: orchestratorContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.test/snapshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "input.txt"), []byte("snapshot execution works\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := job.Spec{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestSnapshotExecutionHelper"},
		WorkingDirectory: filepath.Join(project, "src"),
		Environment:      map[string]string{"COMPUTEHOP_SNAPSHOT_HELPER": "1"}, Executor: job.ExecutorNative,
	}
	first, err := remoteJobs.Submit(ctx, "Snapshot Worker", spec)
	if err != nil {
		t.Fatal(err)
	}
	firstTransferCount := countedWorkspace.puts.Load()
	if firstTransferCount == 0 || !pathInside(first.Spec.WorkingDirectory, workerState) ||
		first.Spec.WorkingDirectory == spec.WorkingDirectory {
		t.Fatalf("first job = %#v, transferred chunks = %d", first, firstTransferCount)
	}
	runner, err := worker.NewRunner(worker.RunnerDependencies{
		Jobs: workerDatabase.Jobs(), Executions: workerDatabase.Executions(), Logs: logs,
		StartProcess: func(spec job.Spec, stdout, stderr io.Writer) (worker.NativeProcess, error) {
			return processes.Start(spec, stdout, stderr)
		},
		RunnerPID: os.Getpid, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	result, err := remoteJobs.ReadLogs(ctx, "Snapshot Worker", first.ID, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.State != job.StateSucceeded || len(result.Page.Records) != 1 ||
		string(result.Page.Records[0].Data) != "snapshot execution works\n" {
		t.Fatalf("job logs = %#v", result)
	}

	second, err := remoteJobs.Submit(ctx, "Snapshot Worker", spec)
	if err != nil {
		t.Fatal(err)
	}
	if countedWorkspace.puts.Load() != firstTransferCount {
		t.Fatalf("unchanged snapshot resent chunks: first = %d, now = %d", firstTransferCount, countedWorkspace.puts.Load())
	}
	if _, err := remoteJobs.Cancel(ctx, "Snapshot Worker", second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotExecutionHelper(t *testing.T) {
	if os.Getenv("COMPUTEHOP_SNAPSHOT_HELPER") != "1" {
		return
	}
	contents, err := os.ReadFile("input.txt")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(contents)
	os.Exit(0)
}

type countingSnapshotWorkspace struct {
	*cas.WorkspaceStore
	puts atomic.Int64
}

func (workspace *countingSnapshotWorkspace) Put(
	ctx context.Context,
	digest snapshot.Digest,
	contents []byte,
) error {
	if err := workspace.WorkspaceStore.Put(ctx, digest, contents); err != nil {
		return err
	}
	workspace.puts.Add(1)
	return nil
}

func pathInside(value, root string) bool {
	relative, err := filepath.Rel(root, value)
	return err == nil && relative != ".." && relative != "." && !filepath.IsAbs(relative)
}

func TestExplicitRemoteJobRoundTripUsesWorkerDurableState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDatabase := openRemoteIntegrationDatabase(t, "worker")
	orchestratorDatabase := openRemoteIntegrationDatabase(t, "orchestrator")
	workerState := t.TempDir()
	logs, err := joblogging.NewStore(workerState, workerDatabase.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	jobService, err := worker.NewJobService(worker.Dependencies{
		Jobs: workerDatabase.Jobs(), Executions: workerDatabase.Executions(), Logs: logs,
		GenerateID: job.NewID, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteHandler, err := worker.NewRemoteHandler(jobService)
	if err != nil {
		t.Fatal(err)
	}

	workerLocal := remoteIntegrationDevice(t, "Gaming PC", device.RoleWorker)
	orchestratorLocal := remoteIntegrationDevice(t, "MacBook", device.RoleOrchestrator)
	pairID, err := trust.NewPairID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pairedAt := time.Now().UTC()
	workerPeer := remoteIntegrationPeer(pairID, workerLocal, pairedAt)
	orchestratorPeer := remoteIntegrationPeer(pairID, orchestratorLocal, pairedAt)
	if err := orchestratorDatabase.Trust().Activate(ctx, workerPeer); err != nil {
		t.Fatal(err)
	}
	if err := workerDatabase.Trust().Activate(ctx, orchestratorPeer); err != nil {
		t.Fatal(err)
	}

	workerEndpoint, err := quictransport.Listen("127.0.0.1:0", workerLocal, workerDatabase.Trust())
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := quictransport.Listen(
		"127.0.0.1:0", orchestratorLocal, orchestratorDatabase.Trust(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			remoteHandler,
		)
	}()

	nearby := remoteIntegrationNearby(t, workerLocal, workerEndpoint.Port())
	remoteJobs, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{nearby}}, nil
		}},
		Trust: orchestratorDatabase.Trust(), Placements: orchestratorDatabase.Placements(),
		Dialer: orchestratorEndpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := remoteJobs.Submit(ctx, "Gaming PC", job.Spec{
		Executable: "echo", Arguments: []string{"hello"}, WorkingDirectory: t.TempDir(),
		Executor: job.ExecutorNative,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if submitted.State != job.StateQueued {
		t.Fatalf("submitted state = %s", submitted.State)
	}
	remembered, err := orchestratorDatabase.Placements().Get(ctx, submitted.ID)
	if err != nil || remembered.WorkerID != workerLocal.Identity.ID() {
		t.Fatalf("orchestrator placement = %#v, %v", remembered, err)
	}
	persisted, err := workerDatabase.Jobs().Get(ctx, submitted.ID)
	if err != nil || persisted.State != job.StateQueued {
		t.Fatalf("worker persisted job = %#v, %v", persisted, err)
	}
	listed, err := remoteJobs.List(ctx, "Gaming PC", job.ListOptions{Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].ID != submitted.ID {
		t.Fatalf("List() = %#v, %v", listed, err)
	}
	cancelled, err := remoteJobs.Cancel(ctx, "", submitted.ID)
	if err != nil || cancelled.State != job.StateCancelled {
		t.Fatalf("Cancel() = %#v, %v", cancelled, err)
	}

	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("worker endpoint shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker endpoint did not stop")
	}
}

func openRemoteIntegrationDatabase(t *testing.T, name string) *sqlite.Database {
	t.Helper()
	database, err := sqlite.Open(
		context.Background(), filepath.Join(t.TempDir(), name+".db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func remoteIntegrationDevice(t *testing.T, name string, role device.Role) session.LocalDevice {
	t.Helper()
	identity, err := device.GenerateIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := device.NewPresenceID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return session.LocalDevice{Identity: identity, Name: name, Role: role, PresenceID: presence}
}

func remoteIntegrationPeer(pairID trust.PairID, local session.LocalDevice, at time.Time) trust.Peer {
	return trust.Peer{
		PairID: pairID, DeviceID: local.Identity.ID(), PublicKey: local.Identity.PublicKey(),
		Name: local.Name, Role: local.Role, State: trust.StateActive,
		PairedAt: at, UpdatedAt: at,
	}
}

func remoteIntegrationNearby(t *testing.T, local session.LocalDevice, port uint16) device.NearbyDevice {
	t.Helper()
	now := time.Now().UTC()
	observation := device.Observation{
		Key: "worker", Announcement: device.Announcement{
			PresenceID: local.PresenceID, Name: local.Name, Role: local.Role,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: port, EndpointReady: true,
		},
		Instance: local.Name, HostName: "localhost.", Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		SeenAt: now, ExpiresAt: now.Add(time.Minute),
	}
	return device.NearbyDevice{Observation: observation, FirstSeenAt: now}
}
