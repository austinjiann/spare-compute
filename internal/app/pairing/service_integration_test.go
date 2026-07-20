package pairing_test

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	pairingapp "github.com/austinjiann/spare-compute/internal/app/pairing"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestTwoDaemonsRequireMatchingLocalConfirmationsBeforePersistingTrust(t *testing.T) {
	worker := localDeviceForPairingTest(t, 11, "Gaming PC", device.RoleWorker)
	orchestrator := localDeviceForPairingTest(t, 12, "MacBook", device.RoleOrchestrator)
	workerEndpoint, err := quictransport.Listen("127.0.0.1:0", worker)
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := quictransport.Listen("127.0.0.1:0", orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	workerDatabase, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer workerDatabase.Close()
	orchestratorDatabase, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorDatabase.Close()
	target := nearbyWorkerForPairingTest(t, worker, workerEndpoint.Port())
	workerService := newPairingServiceForTest(t, worker, workerEndpoint, staticResolver{}, workerDatabase.Trust())
	orchestratorService := newPairingServiceForTest(
		t, orchestrator, orchestratorEndpoint, staticResolver{nearby: target}, orchestratorDatabase.Trust(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveResult := make(chan error, 1)
	go func() { serveResult <- workerService.Run(ctx) }()

	outbound, err := orchestratorService.Begin(context.Background(), "Gaming PC")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	inbound := waitForPairing(t, workerService, func(value trust.Pairing) bool {
		return value.Direction == trust.DirectionInbound
	})
	if outbound.ID != inbound.ID || outbound.Verification != inbound.Verification {
		t.Fatalf("pairing mismatch: outbound=%#v inbound=%#v", outbound, inbound)
	}
	if peers, err := orchestratorService.ListTrusted(context.Background()); err != nil || len(peers) != 0 {
		t.Fatalf("trust existed before confirmation: %#v, %v", peers, err)
	}

	confirmedOutbound, err := orchestratorService.Confirm(context.Background(), outbound.ID.Short())
	if err != nil || !confirmedOutbound.LocalConfirmed || confirmedOutbound.State != trust.PairingWaiting {
		t.Fatalf("orchestrator Confirm() = %#v, %v", confirmedOutbound, err)
	}
	waitForPairing(t, workerService, func(value trust.Pairing) bool { return value.RemoteConfirmed })
	if _, err := workerService.Confirm(context.Background(), inbound.ID.Short()); err != nil {
		t.Fatalf("worker Confirm() error = %v", err)
	}
	waitForPairing(t, workerService, func(value trust.Pairing) bool { return value.State == trust.PairingPaired })
	waitForPairing(t, orchestratorService, func(value trust.Pairing) bool { return value.State == trust.PairingPaired })

	workerPeers, err := workerService.ListTrusted(context.Background())
	if err != nil || len(workerPeers) != 1 || workerPeers[0].DeviceID != orchestrator.Identity.ID() {
		t.Fatalf("worker trust = %#v, %v", workerPeers, err)
	}
	orchestratorPeers, err := orchestratorService.ListTrusted(context.Background())
	if err != nil || len(orchestratorPeers) != 1 || orchestratorPeers[0].DeviceID != worker.Identity.ID() {
		t.Fatalf("orchestrator trust = %#v, %v", orchestratorPeers, err)
	}
	if _, err := orchestratorService.Begin(context.Background(), "Gaming PC"); err == nil {
		t.Fatal("Begin() allowed an already-active peer")
	}
	flippedRole := orchestrator
	flippedRole.Role = device.RoleWorker
	if _, err := pairingapp.NewService(pairingapp.Dependencies{
		Local: flippedRole, Nearby: staticResolver{}, Trust: orchestratorDatabase.Trust(),
		Endpoint: orchestratorEndpoint, Now: time.Now, Lifetime: time.Minute,
	}); !errors.Is(err, trust.ErrConflict) {
		t.Fatalf("NewService() after unsafe role change error = %v", err)
	}
	revoked, err := workerService.Unpair(context.Background(), workerPeers[0].DeviceID.Short())
	if err != nil || revoked.State != trust.StateRevoked {
		t.Fatalf("Unpair() = %#v, %v", revoked, err)
	}

	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pairing service did not stop")
	}
}

type staticResolver struct {
	nearby device.NearbyDevice
}

func (resolver staticResolver) ResolveNearby(context.Context, string) (device.NearbyDevice, error) {
	return resolver.nearby, nil
}

func newPairingServiceForTest(
	t *testing.T,
	local session.LocalDevice,
	endpoint session.PairingEndpoint,
	resolver pairingapp.NearbyResolver,
	repository trust.Repository,
) *pairingapp.Service {
	t.Helper()
	service, err := pairingapp.NewService(pairingapp.Dependencies{
		Local: local, Nearby: resolver, Trust: repository, Endpoint: endpoint,
		Now: time.Now, Lifetime: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func localDeviceForPairingTest(t *testing.T, seed byte, name string, role device.Role) session.LocalDevice {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	presence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{seed}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	return session.LocalDevice{Identity: identity, Name: name, Role: role, PresenceID: presence}
}

func nearbyWorkerForPairingTest(t *testing.T, local session.LocalDevice, port uint16) device.NearbyDevice {
	t.Helper()
	now := time.Now().UTC()
	observation := device.Observation{
		Key: "worker", Announcement: device.Announcement{
			PresenceID: local.PresenceID, Name: local.Name, Role: local.Role,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: port, EndpointReady: true,
		},
		Instance: "worker", HostName: "localhost.", Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		SeenAt: now, ExpiresAt: now.Add(time.Minute),
	}
	return device.NearbyDevice{Observation: observation, FirstSeenAt: now}
}

func waitForPairing(t *testing.T, service *pairingapp.Service, match func(trust.Pairing) bool) trust.Pairing {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		values, err := service.ListPairings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if match(value) {
				return value
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for pairing state")
	return trust.Pairing{}
}
