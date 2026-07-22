package cas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestArtifactManagerCollectsImmutableOutputsAndRestoresWithoutOverwrite(t *testing.T) {
	ctx := context.Background()
	content, err := New(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	repository := &memoryArtifactRepository{bundles: make(map[job.ID]artifact.Bundle)}
	collectedAt := time.Unix(1_900_000_000, 0).UTC()
	manager, err := NewArtifactManager(content, repository, func() time.Time { return collectedAt })
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeArtifactTestFile(t, filepath.Join(workspace, "dist", "app"), []byte("original binary"), 0o755)
	writeArtifactTestFile(t, filepath.Join(workspace, "dist", "report.txt"), []byte("report"), 0o644)
	value := artifactTestJob(t, workspace, []string{"dist"})
	bundle, err := manager.Collect(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.TotalBytes == 0 || !bundle.CollectedAt.Equal(collectedAt) {
		t.Fatalf("bundle = %#v", bundle)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dist", "app"), []byte("mutated later"), 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "results")
	result, err := manager.Restore(ctx, bundle, destination)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(destination, "dist", "app"))
	if err != nil || string(restored) != "original binary" || len(result.Restored) != 2 || len(result.Conflicts) != 0 {
		t.Fatalf("first restore = %#v, contents = %q, error = %v", result, restored, err)
	}
	if got := repository.retrieved[value.ID]; !got.Equal(collectedAt) {
		t.Fatalf("retrieved at = %s, want %s", got, collectedAt)
	}

	if err := os.WriteFile(filepath.Join(destination, "dist", "app"), []byte("keep local"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = manager.Restore(ctx, bundle, destination)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := os.ReadFile(filepath.Join(destination, "dist", "app"))
	if string(local) != "keep local" || len(result.Conflicts) != 1 {
		t.Fatalf("conflict restore = %#v, local = %q", result, local)
	}
	incoming, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(result.Conflicts[0])))
	if err != nil || string(incoming) != "original binary" {
		t.Fatalf("preserved incoming = %q, %v", incoming, err)
	}
}

func TestArtifactManagerRejectsMissingOutputAndUnreferencedChunk(t *testing.T) {
	ctx := context.Background()
	content, _ := New(filepath.Join(t.TempDir(), "content"))
	repository := &memoryArtifactRepository{bundles: make(map[job.ID]artifact.Bundle)}
	manager, _ := NewArtifactManager(content, repository, time.Now)
	value := artifactTestJob(t, t.TempDir(), []string{"missing"})
	if _, err := manager.Collect(ctx, value); !errors.Is(err, snapshot.ErrDeclaredOutputMissing) {
		t.Fatalf("Collect() error = %v", err)
	}
	contents := []byte("output")
	digest := snapshot.Sum(contents)
	if err := content.Put(ctx, digest, contents); err != nil {
		t.Fatal(err)
	}
	bundle := artifact.Bundle{
		JobID: value.ID, CollectedAt: time.Now().UTC(),
		Manifest: snapshot.Manifest{Version: snapshot.ManifestVersion},
	}
	if err := repository.Save(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReadJobChunk(ctx, value.ID, digest); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("ReadJobChunk() error = %v", err)
	}
}

func TestArtifactManagerDoesNotFollowDestinationDirectorySymlinks(t *testing.T) {
	ctx := context.Background()
	content, _ := New(filepath.Join(t.TempDir(), "content"))
	repository := &memoryArtifactRepository{bundles: make(map[job.ID]artifact.Bundle)}
	manager, _ := NewArtifactManager(content, repository, time.Now)
	workspace := t.TempDir()
	writeArtifactTestFile(t, filepath.Join(workspace, "dist", "app"), []byte("artifact"), 0o755)
	bundle, err := manager.Collect(ctx, artifactTestJob(t, workspace, []string{"dist/app"}))
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "results")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(destination, "dist")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := manager.Restore(ctx, bundle, destination); !errors.Is(err, artifact.ErrInvalidDestination) {
		t.Fatalf("Restore() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore escaped through symlink: %v", err)
	}
}

func artifactTestJob(t *testing.T, directory string, outputs []string) job.Job {
	t.Helper()
	value, err := job.New(job.ID("47463925-558d-4a66-aa04-933807415ed0"), job.Spec{
		Executable: "build", WorkingDirectory: directory, Executor: job.ExecutorNative, Outputs: outputs,
	}, time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	value.State = job.StateRunning
	return value
}

func writeArtifactTestFile(t *testing.T, filename string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, mode); err != nil {
		t.Fatal(err)
	}
}

type memoryArtifactRepository struct {
	mu        sync.Mutex
	bundles   map[job.ID]artifact.Bundle
	retrieved map[job.ID]time.Time
}

func (repository *memoryArtifactRepository) Save(_ context.Context, bundle artifact.Bundle) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if existing, ok := repository.bundles[bundle.JobID]; ok {
		existingID, _ := existing.Manifest.ID()
		bundleID, _ := bundle.Manifest.ID()
		if existingID != bundleID {
			return artifact.ErrConflict
		}
		return nil
	}
	repository.bundles[bundle.JobID] = bundle.Clone()
	return nil
}

func (repository *memoryArtifactRepository) Get(_ context.Context, id job.ID) (artifact.Bundle, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	bundle, ok := repository.bundles[id]
	if !ok {
		return artifact.Bundle{}, artifact.ErrNotFound
	}
	return bundle.Clone(), nil
}

func (repository *memoryArtifactRepository) MarkRetrieved(
	_ context.Context,
	id job.ID,
	at time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.retrieved == nil {
		repository.retrieved = make(map[job.ID]time.Time)
	}
	repository.retrieved[id] = at.UTC()
	return nil
}
