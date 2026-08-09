package quic

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestPairingEndpointAuthenticatesBothDeviceKeysAndSharesBinding(t *testing.T) {
	worker := localDeviceForTransportTest(t, 1, "Gaming PC", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 2, "MacBook", device.RoleOrchestrator)
	workerEndpoint, err := Listen("127.0.0.1:0", worker, newTransportTrustRepository())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := Listen("127.0.0.1:0", orchestrator, newTransportTrustRepository())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accepted := make(chan session.PairingChannel, 1)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { accepted <- channel },
			noopRemoteHandler(),
		)
	}()

	target := nearbyForTransportTest(t, worker, workerEndpoint.Port())
	outbound, err := orchestratorEndpoint.DialPairing(context.Background(), target)
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
	workerEndpoint, err := Listen("127.0.0.1:0", worker, newTransportTrustRepository())
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := Listen("127.0.0.1:0", orchestrator, newTransportTrustRepository())
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			noopRemoteHandler(),
		)
	}()
	target := nearbyForTransportTest(t, worker, workerEndpoint.Port())
	forgedPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{9}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	target.Announcement.PresenceID = forgedPresence
	dialContext, stopDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDial()
	if channel, err := orchestratorEndpoint.DialPairing(dialContext, target); err == nil {
		_ = channel.Close()
		t.Fatal("Dial() accepted an endpoint whose live presence did not match discovery")
	}
}

func noopRemoteHandler() remoteprotocol.Handler {
	return remoteprotocol.HandlerFunc(func(
		context.Context,
		*computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		return &computehopv1.RemoteResponse{Error: &computehopv1.RemoteError{
			Code: computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL, Message: "not used",
		}}
	})
}

type transportTrustRepository struct {
	mu    sync.Mutex
	peers map[device.ID]trust.Peer
}

func newTransportTrustRepository(peers ...trust.Peer) *transportTrustRepository {
	repository := &transportTrustRepository{peers: make(map[device.ID]trust.Peer)}
	for _, peer := range peers {
		repository.peers[peer.DeviceID] = peer.Clone()
	}
	return repository
}

func (repository *transportTrustRepository) Activate(_ context.Context, peer trust.Peer) error {
	if err := peer.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.peers[peer.DeviceID] = peer.Clone()
	return nil
}

func (repository *transportTrustRepository) Get(_ context.Context, id device.ID) (trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, ok := repository.peers[id]
	if !ok {
		return trust.Peer{}, trust.ErrNotFound
	}
	return peer.Clone(), nil
}

func (repository *transportTrustRepository) List(context.Context) ([]trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peers := make([]trust.Peer, 0, len(repository.peers))
	for _, peer := range repository.peers {
		peers = append(peers, peer.Clone())
	}
	return peers, nil
}

func (repository *transportTrustRepository) Revoke(
	_ context.Context,
	id device.ID,
	at time.Time,
) (trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, ok := repository.peers[id]
	if !ok {
		return trust.Peer{}, trust.ErrNotFound
	}
	if peer.State == trust.StateRevoked {
		return peer.Clone(), nil
	}
	if at.IsZero() {
		return trust.Peer{}, errors.New("revocation time is required")
	}
	peer.State = trust.StateRevoked
	peer.UpdatedAt = at.UTC()
	peer.RevokedAt = &peer.UpdatedAt
	repository.peers[id] = peer
	return peer.Clone(), nil
}

func (repository *transportTrustRepository) UpdateHints(
	_ context.Context,
	id device.ID,
	hints trust.PeerHints,
) (trust.Peer, error) {
	if err := hints.Validate(); err != nil {
		return trust.Peer{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, ok := repository.peers[id]
	if !ok {
		return trust.Peer{}, trust.ErrNotFound
	}
	peer.Platform = hints.Platform
	peer.Architecture = hints.Architecture
	peer.LogicalCPUCount = hints.LogicalCPUCount
	peer.TotalMemoryBytes = hints.TotalMemoryBytes
	observedAt := hints.ObservedAt.UTC()
	peer.HintsObservedAt = &observedAt
	repository.peers[id] = peer
	return peer.Clone(), nil
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
	f.Fuzz(func(_ *testing.T, contents []byte) {
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
