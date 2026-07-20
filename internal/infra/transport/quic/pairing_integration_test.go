package quic

import (
	"bytes"
	"context"
	"net/netip"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestPairingEndpointAuthenticatesBothDeviceKeysAndSharesBinding(t *testing.T) {
	worker := localDeviceForTransportTest(t, 1, "Gaming PC", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 2, "MacBook", device.RoleOrchestrator)
	workerEndpoint, err := Listen("127.0.0.1:0", worker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := Listen("127.0.0.1:0", orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accepted := make(chan session.PairingChannel, 1)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- workerEndpoint.Run(ctx, func(channel session.PairingChannel) { accepted <- channel })
	}()

	target := nearbyForTransportTest(t, worker, workerEndpoint.Port())
	outbound, err := orchestratorEndpoint.Dial(context.Background(), target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer outbound.Close()
	var inbound session.PairingChannel
	select {
	case inbound = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not accept pairing")
	}
	defer inbound.Close()

	if got := outbound.Peer(); got.ID != worker.Identity.ID() || got.Name != worker.Name || got.Role != worker.Role {
		t.Fatalf("outbound peer = %#v", got)
	}
	if got := inbound.Peer(); got.ID != orchestrator.Identity.ID() || got.Name != orchestrator.Name || got.Role != orchestrator.Role {
		t.Fatalf("inbound peer = %#v", got)
	}
	if !bytes.Equal(outbound.Binding(), inbound.Binding()) || len(outbound.Binding()) != trust.PairingBindingBytes {
		t.Fatal("TLS exporter binding did not match on both endpoints")
	}
	pairID, _, err := trust.DerivePairing(outbound.Binding(), orchestrator.Identity.ID(), worker.Identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := outbound.SendDecision(context.Background(), session.PairingDecision{
		PairingID: pairID, Confirmed: true,
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := inbound.ReceiveDecision(context.Background())
	if err != nil || decision.PairingID != pairID || !decision.Confirmed || decision.Committed {
		t.Fatalf("ReceiveDecision() = %#v, %v", decision, err)
	}
	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop")
	}
}

func TestPairingEndpointRejectsAReplayedPresenceAtAnotherEndpoint(t *testing.T) {
	worker := localDeviceForTransportTest(t, 3, "Actual Worker", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 4, "MacBook", device.RoleOrchestrator)
	workerEndpoint, err := Listen("127.0.0.1:0", worker)
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := Listen("127.0.0.1:0", orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = workerEndpoint.Run(ctx, func(channel session.PairingChannel) { _ = channel.Close() }) }()
	target := nearbyForTransportTest(t, worker, workerEndpoint.Port())
	forgedPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{9}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	target.Announcement.PresenceID = forgedPresence
	dialContext, stopDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDial()
	if channel, err := orchestratorEndpoint.Dial(dialContext, target); err == nil {
		_ = channel.Close()
		t.Fatal("Dial() accepted an endpoint whose live presence did not match discovery")
	}
}

func FuzzPairingFrameDecoder(f *testing.F) {
	valid := &computehopv1.PairingFrame{
		ProtocolVersion: pairingProtocolVersion,
		Payload: &computehopv1.PairingFrame_Decision{Decision: &computehopv1.PairingDecision{
			PairingId: "aaaaaaaaaaaaaaaaaaaaaaaaaa", Confirmed: true,
		}},
	}
	var framed bytes.Buffer
	if err := writeFrame(&framed, valid); err != nil {
		f.Fatal(err)
	}
	f.Add(framed.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 1, 0xff})
	f.Fuzz(func(t *testing.T, contents []byte) {
		_, _ = readFrame(bytes.NewReader(contents))
	})
}

func localDeviceForTransportTest(t *testing.T, seed byte, name string, role device.Role) session.LocalDevice {
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

func nearbyForTransportTest(t *testing.T, local session.LocalDevice, port uint16) device.NearbyDevice {
	t.Helper()
	now := time.Now().UTC()
	observation := device.Observation{
		Key: "test", Announcement: device.Announcement{
			PresenceID: local.PresenceID, Name: local.Name, Role: local.Role,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: port, EndpointReady: true,
		},
		Instance: "test", HostName: "localhost.", Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		SeenAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	return device.NearbyDevice{Observation: observation, FirstSeenAt: now}
}
