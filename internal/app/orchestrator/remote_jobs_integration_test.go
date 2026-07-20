package orchestrator

import (
	"context"
	"crypto/rand"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

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
		Trust: orchestratorDatabase.Trust(), Dialer: orchestratorEndpoint,
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
	persisted, err := workerDatabase.Jobs().Get(ctx, submitted.ID)
	if err != nil || persisted.State != job.StateQueued {
		t.Fatalf("worker persisted job = %#v, %v", persisted, err)
	}
	listed, err := remoteJobs.List(ctx, "Gaming PC", job.ListOptions{Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].ID != submitted.ID {
		t.Fatalf("List() = %#v, %v", listed, err)
	}
	cancelled, err := remoteJobs.Cancel(ctx, "Gaming PC", submitted.ID)
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
