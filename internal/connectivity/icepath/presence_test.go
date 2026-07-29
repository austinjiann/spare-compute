package icepath_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
	"google.golang.org/protobuf/proto"
)

func TestEncryptedPresenceRoundTrip(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{7}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := icepath.NewPresence(testDescription(), 42, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := icepath.SealPresence(secret, access.RouteID, device.RoleWorker, presence)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := icepath.OpenPresence(
		secret, access.RouteID, device.RoleWorker, presence.Generation, ciphertext, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Generation != presence.Generation || opened.SessionID != presence.SessionID ||
		!opened.CreatedAt.Equal(presence.CreatedAt) || !opened.ExpiresAt.Equal(presence.ExpiresAt) ||
		opened.Description.Ufrag != presence.Description.Ufrag ||
		opened.Description.Password != presence.Description.Password ||
		len(opened.Description.Candidates) != len(presence.Description.Candidates) {
		t.Fatalf("opened presence = %#v", opened)
	}
}

func TestEncryptedPresenceRejectsWrongContextAndExpiry(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{8}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := icepath.NewPresence(testDescription(), 9, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := icepath.SealPresence(secret, access.RouteID, device.RoleOrchestrator, presence)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		role       device.Role
		generation uint64
		now        time.Time
	}{
		{role: device.RoleWorker, generation: presence.Generation, now: now},
		{role: device.RoleOrchestrator, generation: presence.Generation + 1, now: now},
		{role: device.RoleOrchestrator, generation: presence.Generation, now: presence.ExpiresAt},
	}
	for index, test := range tests {
		if _, err := icepath.OpenPresence(
			secret, access.RouteID, test.role, test.generation, ciphertext, test.now,
		); !errors.Is(err, icepath.ErrInvalidPresence) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func TestEncryptedPresenceRejectsMalformedPlaintext(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{9}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	message := &connectivityv1.EndpointPresencePayload{
		ProtocolVersion:   icepath.PresenceProtocolVersion + 1,
		Generation:        1,
		SessionId:         bytes.Repeat([]byte{1}, icepath.SessionIDBytes),
		CreatedAtUnixNano: now.UnixNano(),
		ExpiresAtUnixNano: now.Add(time.Minute).UnixNano(),
		Ice: &connectivityv1.ICEPathDescription{
			UsernameFragment: testDescription().Ufrag,
			Password:         testDescription().Password,
			Candidates:       testDescription().Candidates,
		},
	}
	plaintext, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := connectivity.SealPresence(
		secret, access.RouteID, device.RoleWorker, message.Generation, plaintext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := icepath.OpenPresence(
		secret, access.RouteID, device.RoleWorker, message.Generation, ciphertext, now,
	); !errors.Is(err, icepath.ErrInvalidPresence) {
		t.Fatalf("error = %v", err)
	}
}

func testDescription() icepath.Description {
	return icepath.Description{
		Ufrag: "abcd", Password: "abcdefghijklmnopqrstuv",
		Candidates: []string{"1 1 udp 2130706431 192.0.2.1 5000 typ host"},
	}
}
