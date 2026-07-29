package cas

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestManagedStoreEvictsLeastRecentlyUsedChunk(t *testing.T) {
	store, _, clock := newManagedStoreForTest(t)
	first := repeatedChunk('a')
	second := repeatedChunk('b')
	third := repeatedChunk('c')
	putAt(t, store, clock, first)
	putAt(t, store, clock, second)
	clock.Advance(time.Minute)
	if _, err := store.Read(context.Background(), snapshot.Sum(first)); err != nil {
		t.Fatal(err)
	}
	putAt(t, store, clock, third)

	if _, err := store.Read(context.Background(), snapshot.Sum(second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("least recently used Read() error = %v", err)
	}
	for _, contents := range [][]byte{first, third} {
		if _, err := store.Read(context.Background(), snapshot.Sum(contents)); err != nil {
			t.Fatalf("retained Read() error = %v", err)
		}
	}
	stats, err := store.Stats(context.Background())
	if err != nil || stats.Entries != 2 || stats.Bytes != int64(len(first)+len(third)) {
		t.Fatalf("Stats() = %#v, %v", stats, err)
	}
}

func TestManagedStoreProtectsReservationsAndRejectsOversizedActiveSet(t *testing.T) {
	store, _, clock := newManagedStoreForTest(t)
	old := repeatedChunk('o')
	first := repeatedChunk('a')
	second := repeatedChunk('b')
	putAt(t, store, clock, old)
	manifestID := snapshot.Sum([]byte("incoming manifest"))
	if err := store.Reserve(context.Background(), manifestID, []snapshot.Digest{
		snapshot.Sum(first), snapshot.Sum(second),
	}); err != nil {
		t.Fatal(err)
	}
	putAt(t, store, clock, first)
	putAt(t, store, clock, second)
	if _, err := store.Read(context.Background(), snapshot.Sum(old)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreserved old chunk error = %v", err)
	}
	store.ReleaseReservation(manifestID)

	release := store.BeginUse()
	defer release()
	for _, contents := range [][]byte{first, second} {
		if _, err := store.Read(context.Background(), snapshot.Sum(contents)); err != nil {
			t.Fatal(err)
		}
	}
	third := repeatedChunk('c')
	clock.Advance(time.Minute)
	err := store.Put(context.Background(), snapshot.Sum(third), third)
	if !errors.Is(err, contentcache.ErrQuotaExceeded) {
		t.Fatalf("oversized active Put() error = %v", err)
	}
	release()
	stats, err := store.Stats(context.Background())
	if err != nil || stats.Bytes > contentcache.MinimumMaximumBytes {
		t.Fatalf("post-release Stats() = %#v, %v", stats, err)
	}
}

func TestManagedStoreNeverEvictsArtifactOrRunningJobContent(t *testing.T) {
	store, database, clock := newManagedStoreForTest(t)
	protected := repeatedChunk('p')
	other := repeatedChunk('o')
	third := repeatedChunk('t')
	putAt(t, store, clock, protected)

	value, err := job.New(
		job.ID("4daed5a4-6230-43a0-a235-bd9b4546664c"),
		job.Spec{Executable: "render", WorkingDirectory: "/work", Executor: job.ExecutorNative},
		clock.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Jobs().Create(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	bundle := artifact.Bundle{
		JobID: value.ID,
		Manifest: snapshot.Manifest{
			Version: snapshot.ManifestVersion,
			Files: []snapshot.File{{
				Path: "dist/result", Mode: 0o644, Size: int64(len(protected)),
				Chunks: []snapshot.Chunk{{Digest: snapshot.Sum(protected), Size: uint32(len(protected))}},
			}},
			TotalBytes: int64(len(protected)),
		},
		CollectedAt: clock.Now(),
	}
	if err := database.Artifacts().Save(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	putAt(t, store, clock, other)
	putAt(t, store, clock, third)
	if _, err := store.Read(context.Background(), snapshot.Sum(protected)); err != nil {
		t.Fatalf("artifact chunk was evicted: %v", err)
	}
	if _, err := store.Read(context.Background(), snapshot.Sum(other)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unprotected artifact-era chunk error = %v", err)
	}

	running := advanceJobForCacheTest(t, database, value, job.StateValidating)
	running = advanceJobForCacheTest(t, database, running, job.StateQueued)
	running = advanceJobForCacheTest(t, database, running, job.StateStarting)
	running = advanceJobForCacheTest(t, database, running, job.StateRunning)
	fourth := repeatedChunk('f')
	err = store.Put(context.Background(), snapshot.Sum(fourth), fourth)
	if !errors.Is(err, contentcache.ErrQuotaExceeded) {
		t.Fatalf("running-job Put() error = %v", err)
	}
	stats, err := store.Stats(context.Background())
	if err != nil || stats.ProtectedEntries != stats.Entries {
		t.Fatalf("running Stats() = %#v, %v", stats, err)
	}
	_ = advanceJobForCacheTest(t, database, running, job.StateCancelled)
	if err := store.Enforce(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err = store.Stats(context.Background())
	if err != nil || stats.Bytes > contentcache.MinimumMaximumBytes {
		t.Fatalf("terminal Stats() = %#v, %v", stats, err)
	}
}

func TestManagedStoreReconcilesLegacyFilesOnRestart(t *testing.T) {
	stateDir := t.TempDir()
	contentDir, err := paths.ContentStoreDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := New(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("legacy verified content")
	if err := legacy.Put(context.Background(), snapshot.Sum(contents), contents); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Dir(mustChunkPath(t, legacy, snapshot.Sum(contents)))
	incoming := filepath.Join(shard, ".incoming-abandoned")
	if err := os.WriteFile(incoming, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(context.Background(), filepath.Join(stateDir, "computehop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	clock := &managedTestClock{current: time.Unix(1_900_000_000, 0).UTC()}
	managed, err := NewManaged(context.Background(), Config{
		Root: contentDir, Index: database.ContentCache(),
		MaximumBytes: contentcache.MinimumMaximumBytes, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := managed.Stats(context.Background())
	if err != nil || stats.Entries != 1 || stats.Bytes != int64(len(contents)) {
		t.Fatalf("reconciled Stats() = %#v, %v", stats, err)
	}
	if _, err := os.Lstat(incoming); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned temporary file remains: %v", err)
	}
}

type managedTestClock struct {
	mu      sync.Mutex
	current time.Time
}

func (clock *managedTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.current
}

func (clock *managedTestClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.current = clock.current.Add(duration)
	clock.mu.Unlock()
}

func newManagedStoreForTest(t *testing.T) (*Store, *sqlite.Database, *managedTestClock) {
	t.Helper()
	stateDir := t.TempDir()
	database, err := sqlite.Open(context.Background(), filepath.Join(stateDir, "computehop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	contentDir, err := paths.ContentStoreDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	clock := &managedTestClock{current: time.Unix(1_900_000_000, 0).UTC()}
	store, err := NewManaged(context.Background(), Config{
		Root: contentDir, Index: database.ContentCache(),
		MaximumBytes: contentcache.MinimumMaximumBytes, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, database, clock
}

func repeatedChunk(value byte) []byte {
	return bytes.Repeat([]byte{value}, 400<<10)
}

func putAt(t *testing.T, store *Store, clock *managedTestClock, contents []byte) {
	t.Helper()
	clock.Advance(time.Minute)
	if err := store.Put(context.Background(), snapshot.Sum(contents), contents); err != nil {
		t.Fatal(err)
	}
}

func advanceJobForCacheTest(
	t *testing.T,
	database *sqlite.Database,
	current job.Job,
	to job.State,
) job.Job {
	t.Helper()
	next, err := database.Jobs().ApplyTransition(context.Background(), job.Transition{
		JobID: current.ID, From: current.State, To: to, At: current.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func mustChunkPath(t *testing.T, store *Store, digest snapshot.Digest) string {
	t.Helper()
	path, err := store.chunkPath(digest)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
