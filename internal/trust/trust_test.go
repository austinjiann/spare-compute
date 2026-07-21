package trust

import (
	"bytes"
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
