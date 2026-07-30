package sqlite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestTrustRepositoryPersistsRevokesAndReplacesOneOrchestrator(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	first := trustedPeerForTest(t, 1, device.RoleOrchestrator)
	if err := database.Trust().Activate(context.Background(), first); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	loaded, err := database.Trust().Get(context.Background(), first.DeviceID)
	if err != nil || loaded.DeviceID != first.DeviceID || !bytes.Equal(loaded.PublicKey, first.PublicKey) {
		t.Fatalf("Get() = %#v, %v", loaded, err)
	}
	second := trustedPeerForTest(t, 2, device.RoleOrchestrator)
	if err := database.Trust().Activate(context.Background(), second); !errors.Is(err, trust.ErrConflict) {
		t.Fatalf("second orchestrator Activate() error = %v", err)
	}
	revokedAt := first.UpdatedAt.Add(time.Minute)
	revoked, err := database.Trust().Revoke(context.Background(), first.DeviceID, revokedAt)
	if err != nil || revoked.State != trust.StateRevoked || revoked.RevokedAt == nil ||
		len(revoked.ConnectivitySecret) != 0 {
		t.Fatalf("Revoke() = %#v, %v", revoked, err)
	}
	if err := database.Trust().Activate(context.Background(), second); err != nil {
		t.Fatalf("replacement Activate() error = %v", err)
	}
	peers, err := database.Trust().List(context.Background())
	if err != nil || len(peers) != 2 || peers[0].DeviceID != second.DeviceID {
		t.Fatalf("List() = %#v, %v", peers, err)
	}
}

func TestTrustRepositorySurvivesDatabaseRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computehop.db")
	firstDatabase, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := trustedPeerForTest(t, 5, device.RoleWorker)
	if err := firstDatabase.Trust().Activate(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := firstDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	secondDatabase, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDatabase.Close()
	got, err := secondDatabase.Trust().Get(context.Background(), want.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PairID != want.PairID || got.DeviceID != want.DeviceID ||
		!bytes.Equal(got.PublicKey, want.PublicKey) ||
		!bytes.Equal(got.ConnectivitySecret, want.ConnectivitySecret) {
		t.Fatalf("trust after restart = %#v, want %#v", got, want)
	}
}

func TestTrustRepositoryPersistsNewestHints(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	peer := trustedPeerForTest(t, 6, device.RoleWorker)
	if err := database.Trust().Activate(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	observedAt := peer.UpdatedAt.Add(time.Minute)
	got, err := database.Trust().UpdateHints(context.Background(), peer.DeviceID, trust.PeerHints{
		Platform: "linux", Architecture: "amd64",
		LogicalCPUCount: 32, TotalMemoryBytes: 64 << 30,
		ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != "linux" || got.Architecture != "amd64" ||
		got.LogicalCPUCount != 32 || got.TotalMemoryBytes != 64<<30 ||
		got.HintsObservedAt == nil || !got.HintsObservedAt.Equal(observedAt) {
		t.Fatalf("updated hints = %#v", got)
	}
	got, err = database.Trust().UpdateHints(context.Background(), peer.DeviceID, trust.PeerHints{
		Platform: "darwin", Architecture: "arm64",
		LogicalCPUCount: 8, TotalMemoryBytes: 16 << 30,
		ObservedAt: observedAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != "linux" || got.Architecture != "amd64" ||
		got.LogicalCPUCount != 32 || got.TotalMemoryBytes != 64<<30 {
		t.Fatalf("stale hints replaced newer hints: %#v", got)
	}
}

func TestTrustRepositoryKeepsRevokedPeerHintsButDoesNotUpdateThem(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	peer := trustedPeerForTest(t, 7, device.RoleWorker)
	if err := database.Trust().Activate(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	observedAt := peer.UpdatedAt.Add(time.Minute)
	if _, err := database.Trust().UpdateHints(context.Background(), peer.DeviceID, trust.PeerHints{
		Platform: "windows", Architecture: "amd64",
		LogicalCPUCount: 24, TotalMemoryBytes: 32 << 30,
		ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	revoked, err := database.Trust().Revoke(context.Background(), peer.DeviceID, observedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := database.Trust().UpdateHints(context.Background(), peer.DeviceID, trust.PeerHints{
		Platform: "linux", Architecture: "arm64",
		LogicalCPUCount: 4, TotalMemoryBytes: 8 << 30,
		ObservedAt: observedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trust.StateRevoked || got.Platform != revoked.Platform ||
		got.Architecture != revoked.Architecture || got.LogicalCPUCount != revoked.LogicalCPUCount {
		t.Fatalf("revoked peer hints changed: %#v", got)
	}
}

func trustedPeerForTest(t *testing.T, seed byte, role device.Role) trust.Peer {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{seed}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	pairedAt := time.Unix(1_700_000_000+int64(seed), 0).UTC()
	return trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		ConnectivitySecret: bytes.Repeat([]byte{seed}, trust.ConnectivitySecretBytes),
		Name:               "Peer " + string(rune('A'+seed)), Role: role, State: trust.StateActive,
		PairedAt: pairedAt, UpdatedAt: pairedAt,
	}
}
