package trust

import (
	"bytes"
	"net/netip"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

func TestPeerValidatesPinnedPublicKey(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(make([]byte, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := NewPairID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	pairedAt := time.Unix(1_700_000_000, 0).UTC()
	peer := Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: StateActive,
		PairedAt: pairedAt, UpdatedAt: pairedAt,
	}
	if err := peer.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	peer.PublicKey[0] ^= 0xff
	if err := peer.Validate(); err == nil {
		t.Fatal("Validate() accepted a public key that did not match the device ID")
	}
}

func TestMatchNearbyHintsOnlyUsesUnambiguousTrustedWorkerNames(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	build := peerForTest(t, 10, device.RoleWorker, "Build PC")
	render := peerForTest(t, 11, device.RoleWorker, "Render PC")
	otherRender := peerForTest(t, 12, device.RoleWorker, "Render PC")
	orchestrator := peerForTest(t, 13, device.RoleOrchestrator, "Control Mac")

	got := MatchNearbyHints([]Peer{build, render, otherRender, orchestrator}, []device.NearbyDevice{
		nearbyHintForTest(t, "Build PC", now, "linux", "amd64", 32, 64<<30),
		nearbyHintForTest(t, "Render PC", now, "windows", "amd64", 24, 32<<30),
		nearbyHintForTest(t, "Control Mac", now, "darwin", "arm64", 12, 32<<30),
	})

	if len(got) != 1 {
		t.Fatalf("hint matches = %#v", got)
	}
	hints := got[build.DeviceID]
	if hints.Platform != "linux" || hints.Architecture != "amd64" ||
		hints.LogicalCPUCount != 32 || hints.TotalMemoryBytes != 64<<30 ||
		!hints.ObservedAt.Equal(now) {
		t.Fatalf("hints = %#v", hints)
	}
}

func peerForTest(t *testing.T, seed byte, role device.Role, name string) Peer {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := NewPairID(bytes.NewReader(bytes.Repeat([]byte{seed}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000+int64(seed), 0).UTC()
	return Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: name, Role: role, State: StateActive,
		PairedAt: at, UpdatedAt: at,
	}
}

func nearbyHintForTest(
	t *testing.T,
	name string,
	seenAt time.Time,
	platform string,
	architecture string,
	logicalCPUCount uint32,
	totalMemoryBytes uint64,
) device.NearbyDevice {
	t.Helper()
	presence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{name[0]}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	return device.NearbyDevice{
		Observation: device.Observation{
			Key: device.ObservationKey(name + "|worker.local.|47823"),
			Announcement: device.Announcement{
				PresenceID: presence, Name: name, Role: device.RoleWorker,
				ProtocolVersion: device.DiscoveryProtocolVersion, Port: 47823,
				EndpointReady: true, Platform: platform, Architecture: architecture,
				LogicalCPUCount: logicalCPUCount, TotalMemoryBytes: totalMemoryBytes,
			},
			Instance: name, HostName: "worker.local.",
			Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.20")},
			SeenAt:    seenAt, ExpiresAt: seenAt.Add(time.Minute),
		},
		FirstSeenAt: seenAt,
	}
}

func TestDerivePairingIsConnectionAndRoleOrderBound(t *testing.T) {
	first, err := device.GenerateIdentity(bytes.NewReader(make([]byte, 64)))
	if err != nil {
		t.Fatal(err)
	}
	secondSeed := bytes.Repeat([]byte{1}, 64)
	second, err := device.GenerateIdentity(bytes.NewReader(secondSeed))
	if err != nil {
		t.Fatal(err)
	}
	binding := bytes.Repeat([]byte{2}, PairingBindingBytes)
	id, code, err := DerivePairing(binding, first.ID(), second.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !id.Valid() || !validVerificationCode(code) {
		t.Fatalf("invalid derivation: id=%q code=%q", id, code)
	}
	secret, err := DeriveConnectivitySecret(binding, first.ID(), second.ID())
	if err != nil || !secret.Valid() {
		t.Fatalf("invalid connectivity secret: length=%d error=%v", len(secret), err)
	}
	reversedID, reversedCode, err := DerivePairing(binding, second.ID(), first.ID())
	if err != nil {
		t.Fatal(err)
	}
	if reversedID == id || reversedCode == code {
		t.Fatal("pairing derivation did not bind initiator/responder ordering")
	}
	reversedSecret, err := DeriveConnectivitySecret(binding, second.ID(), first.ID())
	if err != nil || bytes.Equal(reversedSecret, secret) {
		t.Fatal("connectivity secret did not bind initiator/responder ordering")
	}
	changed := append([]byte(nil), binding...)
	changed[0] ^= 1
	changedID, changedCode, err := DerivePairing(changed, first.ID(), second.ID())
	if err != nil {
		t.Fatal(err)
	}
	if changedID == id || changedCode == code {
		t.Fatal("pairing derivation did not bind the connection")
	}
	changedSecret, err := DeriveConnectivitySecret(changed, first.ID(), second.ID())
	if err != nil || bytes.Equal(changedSecret, secret) {
		t.Fatal("connectivity secret did not bind the connection")
	}
}
