package connectivity

import (
	"bytes"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestDeriveAccessIsStableOnlyWithinOneCredentialWindow(t *testing.T) {
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{7}, trust.ConnectivitySecretBytes))
	start := time.Date(2026, time.July, 21, 1, 2, 3, 0, time.UTC)
	first, err := DeriveAccess(secret, start)
	if err != nil {
		t.Fatal(err)
	}
	same, err := DeriveAccess(secret, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.RouteID != same.RouteID || first.Token != same.Token || first.ExpiresAt != same.ExpiresAt {
		t.Fatalf("same credential window changed: %#v %#v", first, same)
	}
	next, err := DeriveAccess(secret, first.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if next.RouteID == first.RouteID || next.Token == first.Token || !next.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("next credential window reused access: %#v %#v", first, next)
	}
}

func TestDeriveAccessIsPairScopedAndRejectsInvalidMaterial(t *testing.T) {
	at := time.Date(2026, time.July, 21, 1, 2, 3, 0, time.UTC)
	first, err := DeriveAccess(bytes.Repeat([]byte{1}, trust.ConnectivitySecretBytes), at)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveAccess(bytes.Repeat([]byte{2}, trust.ConnectivitySecretBytes), at)
	if err != nil {
		t.Fatal(err)
	}
	if first.RouteID == second.RouteID || first.Token == second.Token {
		t.Fatal("different pair secrets produced the same access")
	}
	if _, err := DeriveAccess(nil, at); err == nil {
		t.Fatal("DeriveAccess() accepted a missing secret")
	}
	if _, err := DeriveAccess(bytes.Repeat([]byte{1}, trust.ConnectivitySecretBytes), time.Time{}); err == nil {
		t.Fatal("DeriveAccess() accepted a zero timestamp")
	}
}
