package mapper

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
	"google.golang.org/protobuf/proto"
)

func TestTrustProtocolRoundTripsValidatedValues(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{6}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{7}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	pairing := trust.Pairing{
		ID: pairID, PeerID: identity.ID(), PeerPublicKey: identity.PublicKey(),
		PeerName: "Worker", PeerRole: device.RoleWorker,
		Verification: "0123-4567-89AB-CDEF", Direction: trust.DirectionOutbound,
		State: trust.PairingWaiting, LocalConfirmed: true,
		StartedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	pairingMessage, err := PairingToProto(pairing)
	if err != nil {
		t.Fatal(err)
	}
	decodedPairing, err := PairingFromProto(pairingMessage)
	if err != nil || !reflect.DeepEqual(decodedPairing, pairing) {
		t.Fatalf("PairingFromProto() = %#v, %v", decodedPairing, err)
	}

	peer := trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		ConnectivitySecret: bytes.Repeat([]byte{9}, trust.ConnectivitySecretBytes),
		Name:               "Worker", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: now, UpdatedAt: now,
	}
	peerMessage, err := TrustedPeerToProto(peer)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := proto.Marshal(peerMessage)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, peer.ConnectivitySecret) {
		t.Fatal("trusted-device UI message exposed the connectivity secret")
	}
	decodedPeer, err := TrustedPeerFromProto(peerMessage)
	peer.ConnectivitySecret = nil
	if err != nil || !reflect.DeepEqual(decodedPeer, peer) {
		t.Fatalf("TrustedPeerFromProto() = %#v, %v", decodedPeer, err)
	}
}
