package orchestrator

import (
	"context"
	"crypto/rand"
	"net"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestRemoteJobRoundTripFallsBackToSupervisedICEPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workerDatabase := openRemoteIntegrationDatabase(t, "remote-worker")
	orchestratorDatabase := openRemoteIntegrationDatabase(t, "remote-orchestrator")
	logs, err := joblogging.NewStore(t.TempDir(), workerDatabase.Executions(), time.Now)
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

	remoteJobs, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true}, nil
		}},
		Trust: orchestratorDatabase.Trust(), Placements: orchestratorDatabase.Placements(),
		Dialer: orchestratorEndpoint, Remote: orchestratorManager,
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := remoteJobs.Submit(ctx, "Internet Worker", job.Spec{
		Executable: "echo", Arguments: []string{"over ICE"}, WorkingDirectory: t.TempDir(),
		Executor: job.ExecutorNative,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if submitted.State != job.StateQueued {
		t.Fatalf("submitted state = %s", submitted.State)
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
