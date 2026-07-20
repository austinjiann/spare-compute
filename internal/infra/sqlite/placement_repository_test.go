package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/placement"
)

func TestPlacementRepositoryPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computehop.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	peer := trustedPeerForTest(t, 11, device.RoleWorker)
	if err := first.Trust().Activate(ctx, peer); err != nil {
		t.Fatal(err)
	}
	want := placement.Placement{
		JobID: mustJobID(t, 41), WorkerID: peer.DeviceID, PlacedAt: testTime(4),
	}
	if err := first.Placements().Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.Placements().Get(ctx, want.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != want {
		t.Fatalf("Get() = %#v, want %#v", got, want)
	}
}

func TestPlacementRepositoryCreateIsIdempotentForSameWorker(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	peer := trustedPeerForTest(t, 12, device.RoleWorker)
	if err := database.Trust().Activate(ctx, peer); err != nil {
		t.Fatal(err)
	}
	first := placement.Placement{
		JobID: mustJobID(t, 42), WorkerID: peer.DeviceID, PlacedAt: testTime(5),
	}
	if err := database.Placements().Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	repeated := first
	repeated.PlacedAt = repeated.PlacedAt.Add(time.Hour)
	if err := database.Placements().Create(ctx, repeated); err != nil {
		t.Fatalf("repeated Create() error = %v", err)
	}
	got, err := database.Placements().Get(ctx, first.JobID)
	if err != nil || got != first {
		t.Fatalf("Get() = %#v, %v; want original %#v", got, err, first)
	}
}

func TestPlacementRepositoryRejectsDifferentWorkerAndUntrustedTarget(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	firstPeer := trustedPeerForTest(t, 13, device.RoleWorker)
	secondPeer := trustedPeerForTest(t, 14, device.RoleWorker)
	if err := database.Trust().Activate(ctx, firstPeer); err != nil {
		t.Fatal(err)
	}
	if err := database.Trust().Activate(ctx, secondPeer); err != nil {
		t.Fatal(err)
	}
	value := placement.Placement{
		JobID: mustJobID(t, 43), WorkerID: firstPeer.DeviceID, PlacedAt: testTime(6),
	}
	if err := database.Placements().Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	value.WorkerID = secondPeer.DeviceID
	if err := database.Placements().Create(ctx, value); !errors.Is(err, placement.ErrConflict) {
		t.Fatalf("Create(different worker) error = %v, want ErrConflict", err)
	}

	untrusted := placement.Placement{
		JobID: mustJobID(t, 44), WorkerID: trustedPeerForTest(t, 15, device.RoleWorker).DeviceID,
		PlacedAt: testTime(7),
	}
	if err := database.Placements().Create(ctx, untrusted); !errors.Is(err, placement.ErrConflict) {
		t.Fatalf("Create(untrusted worker) error = %v, want ErrConflict", err)
	}
}

func TestPlacementRepositoryValidatesAndReportsMissing(t *testing.T) {
	repository := openTestDatabase(t).Placements()
	ctx := context.Background()
	if err := repository.Create(ctx, placement.Placement{}); !errors.Is(err, placement.ErrInvalid) {
		t.Fatalf("Create(invalid) error = %v", err)
	}
	missing := mustJobID(t, 45)
	if _, err := repository.Get(ctx, missing); !errors.Is(err, placement.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
	if _, err := repository.Get(ctx, job.ID("bad")); !errors.Is(err, job.ErrInvalidID) {
		t.Fatalf("Get(invalid) error = %v", err)
	}
}
