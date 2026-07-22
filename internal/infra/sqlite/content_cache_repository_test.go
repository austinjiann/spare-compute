package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestContentCacheRepositoryReconcilesOrdersAndProtectsArtifacts(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	oldContents := []byte("old")
	newContents := []byte("new content")
	staleContents := []byte("stale")
	oldDigest := snapshot.Sum(oldContents)
	newDigest := snapshot.Sum(newContents)
	staleDigest := snapshot.Sum(staleContents)
	base := time.Unix(1_900_000_000, 0).UTC()

	for _, entry := range []contentcache.Entry{
		{Digest: staleDigest, Size: int64(len(staleContents)), LastAccessed: base.Add(-time.Hour)},
		{Digest: newDigest, Size: int64(len(newContents)), LastAccessed: base},
	} {
		if err := database.ContentCache().Record(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.ContentCache().Reconcile(ctx, []contentcache.Entry{
		{Digest: oldDigest, Size: int64(len(oldContents)), LastAccessed: base.Add(-2 * time.Hour)},
		{Digest: newDigest, Size: int64(len(newContents)), LastAccessed: base.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	candidates, err := database.ContentCache().EvictionCandidates(ctx)
	if err != nil || len(candidates) != 2 || candidates[0].Digest != oldDigest || candidates[1].Digest != newDigest {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}

	value := newArtifactRepositoryJob(t, base)
	if err := database.Jobs().Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	bundle := artifact.Bundle{
		JobID: value.ID,
		Manifest: snapshot.Manifest{
			Version: snapshot.ManifestVersion,
			Files: []snapshot.File{{
				Path: "dist/result", Mode: 0o644, Size: int64(len(newContents)),
				Chunks: []snapshot.Chunk{{Digest: newDigest, Size: uint32(len(newContents))}},
			}},
			TotalBytes: int64(len(newContents)),
		},
		CollectedAt: base.Add(time.Minute),
	}
	if err := database.Artifacts().Save(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	candidates, err = database.ContentCache().EvictionCandidates(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Digest != oldDigest {
		t.Fatalf("protected candidates = %#v, %v", candidates, err)
	}
	stats, err := database.ContentCache().Stats(ctx)
	if err != nil || stats.Entries != 2 || stats.ProtectedEntries != 1 ||
		stats.Bytes != int64(len(oldContents)+len(newContents)) ||
		stats.ProtectedBytes != int64(len(newContents)) {
		t.Fatalf("stats = %#v, %v", stats, err)
	}
	if err := database.Artifacts().MarkRetrieved(ctx, value.ID, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = database.ContentCache().EvictionCandidates(ctx)
	if err != nil || len(candidates) != 2 || candidates[0].Digest != oldDigest || candidates[1].Digest != newDigest {
		t.Fatalf("retrieved candidates = %#v, %v", candidates, err)
	}
	stats, err = database.ContentCache().Stats(ctx)
	if err != nil || stats.ProtectedEntries != 0 || stats.ProtectedBytes != 0 {
		t.Fatalf("retrieved stats = %#v, %v", stats, err)
	}
	if err := database.ContentCache().Delete(ctx, oldDigest); err != nil {
		t.Fatal(err)
	}
	stats, err = database.ContentCache().Stats(ctx)
	if err != nil || stats.Entries != 1 || stats.Bytes != int64(len(newContents)) {
		t.Fatalf("stats after delete = %#v, %v", stats, err)
	}
}

func newArtifactRepositoryJob(t *testing.T, now time.Time) job.Job {
	t.Helper()
	value, err := job.New(
		job.ID("861a79c7-8fdb-49dd-92a4-ded981f175d5"),
		job.Spec{Executable: "render", WorkingDirectory: "/work", Executor: job.ExecutorNative},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
