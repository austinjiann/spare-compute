package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestArtifactRepositoryPersistsValidatesAndDeduplicatesBundle(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	value := newTestJob(t, 17, testTime(1))
	if err := database.Jobs().Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	contents := []byte("artifact contents")
	bundle := artifact.Bundle{
		JobID: value.ID,
		Manifest: snapshot.Manifest{
			Version: snapshot.ManifestVersion,
			Files: []snapshot.File{{
				Path: "dist/app", Mode: 0o755, Size: int64(len(contents)),
				Chunks: []snapshot.Chunk{{Digest: snapshot.Sum(contents), Size: uint32(len(contents))}},
			}},
			TotalBytes: int64(len(contents)),
		},
		CollectedAt: time.Unix(1_900_000_000, 0).UTC(),
	}
	if err := database.Artifacts().Save(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	if err := database.Artifacts().Save(ctx, bundle.Clone()); err != nil {
		t.Fatalf("idempotent Save() error = %v", err)
	}
	loaded, err := database.Artifacts().Get(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantID, _ := bundle.Manifest.ID()
	gotID, _ := loaded.Manifest.ID()
	if gotID != wantID || !loaded.CollectedAt.Equal(bundle.CollectedAt) {
		t.Fatalf("Get() = %#v", loaded)
	}

	changed := bundle.Clone()
	changed.CollectedAt = changed.CollectedAt.Add(time.Second)
	if err := database.Artifacts().Save(ctx, changed); err != nil {
		t.Fatalf("timestamp-only retry Save() error = %v", err)
	}
	changed.Manifest.Files[0].Path = "dist/other-app"
	if err := database.Artifacts().Save(ctx, changed); !errors.Is(err, artifact.ErrConflict) {
		t.Fatalf("changed-content Save() error = %v", err)
	}
	if _, err := database.Artifacts().Get(ctx, mustJobID(t, 18)); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("missing Get() error = %v", err)
	}
}
