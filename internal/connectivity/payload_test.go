package connectivity_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestPayloadRoundTripAndContextBinding(t *testing.T) {
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{3}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, testTime())
	if err != nil {
		t.Fatal(err)
	}
	context := connectivity.PayloadContext{
		Kind: connectivity.PayloadPresence, RouteID: access.RouteID, Sender: device.RoleWorker,
		Recipient: device.RoleOrchestrator, Generation: 42,
	}
	plaintext := []byte("private ICE candidates")
	ciphertext, err := connectivity.SealPayload(secret, context, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := connectivity.OpenPayload(secret, context, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) || bytes.Contains(ciphertext, plaintext) {
		t.Fatalf("opened = %q, ciphertext = %x", opened, ciphertext)
	}

	wrongContext := context
	wrongContext.Generation++
	if _, err := connectivity.OpenPayload(secret, wrongContext, ciphertext); !errors.Is(err, connectivity.ErrInvalidEncryptedPayload) {
		t.Fatalf("wrong context error = %v", err)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := connectivity.OpenPayload(secret, context, tampered); !errors.Is(err, connectivity.ErrInvalidEncryptedPayload) {
		t.Fatalf("tamper error = %v", err)
	}
	otherAccess, err := connectivity.DeriveAccess(secret, testTime().Add(connectivity.CredentialLifetime))
	if err != nil {
		t.Fatal(err)
	}
	wrongRoute := context
	wrongRoute.RouteID = otherAccess.RouteID
	if _, err := connectivity.OpenPayload(secret, wrongRoute, ciphertext); !errors.Is(err, connectivity.ErrInvalidEncryptedPayload) {
		t.Fatalf("wrong route error = %v", err)
	}
}

func TestPayloadUsesFreshNonces(t *testing.T) {
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{4}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, testTime())
	if err != nil {
		t.Fatal(err)
	}
	context := connectivity.PayloadContext{
		Kind: connectivity.PayloadSignal, RouteID: access.RouteID,
		Sender: device.RoleOrchestrator, Recipient: device.RoleWorker,
	}
	first, err := connectivity.SealPayload(secret, context, []byte("offer"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := connectivity.SealPayload(secret, context, []byte("offer"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ciphertexts reused a nonce")
	}
}

func TestPayloadRejectsInvalidInputs(t *testing.T) {
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{5}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, testTime())
	if err != nil {
		t.Fatal(err)
	}
	valid := connectivity.PayloadContext{
		Kind: connectivity.PayloadSignal, RouteID: access.RouteID,
		Sender: device.RoleOrchestrator, Recipient: device.RoleWorker,
	}
	tests := []struct {
		name    string
		secret  trust.ConnectivitySecret
		context connectivity.PayloadContext
		value   []byte
	}{
		{name: "secret", context: valid, value: []byte("value")},
		{name: "empty", secret: secret, context: valid},
		{name: "same role", secret: secret, context: connectivity.PayloadContext{
			Kind: connectivity.PayloadSignal, RouteID: access.RouteID,
			Sender: device.RoleWorker, Recipient: device.RoleWorker,
		}, value: []byte("value")},
		{name: "presence generation", secret: secret, context: connectivity.PayloadContext{
			Kind: connectivity.PayloadPresence, RouteID: access.RouteID,
			Sender: device.RoleWorker, Recipient: device.RoleOrchestrator,
		}, value: []byte("value")},
		{name: "signal generation", secret: secret, context: connectivity.PayloadContext{
			Kind: connectivity.PayloadSignal, RouteID: access.RouteID, Sender: device.RoleWorker,
			Recipient: device.RoleOrchestrator, Generation: 1,
		}, value: []byte("value")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := connectivity.SealPayload(test.secret, test.context, test.value); !errors.Is(err, connectivity.ErrInvalidEncryptedPayload) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func testTime() time.Time {
	return time.Date(2026, time.July, 22, 6, 0, 0, 0, time.UTC)
}
