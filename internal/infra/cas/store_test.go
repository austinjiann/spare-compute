package cas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestStoreVerifiesDeduplicatesAndDetectsCorruption(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	contents := []byte("verified chunk")
	digest := snapshot.Sum(contents)
	if err := store.Put(ctx, digest, contents); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, digest, contents); err != nil {
		t.Fatalf("deduplicated Put() error = %v", err)
	}
	loaded, err := store.Read(ctx, digest)
	if err != nil || string(loaded) != string(contents) {
		t.Fatalf("Read() = %q, %v", loaded, err)
	}
	other := snapshot.Sum([]byte("other"))
	missing, err := store.Missing(ctx, []snapshot.Digest{other, digest, other})
	if err != nil || len(missing) != 1 || missing[0] != other {
		t.Fatalf("Missing() = %#v, %v", missing, err)
	}
	if err := store.Put(ctx, digest, []byte("tampered")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong digest Put() error = %v", err)
	}

	filename, err := store.chunkPath(digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("disk corruption"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, digest); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt Read() error = %v", err)
	}
	missing, err = store.Missing(ctx, []snapshot.Digest{digest})
	if err != nil || len(missing) != 1 || missing[0] != digest {
		t.Fatalf("Missing(corrupt) = %#v, %v", missing, err)
	}
	if err := store.Put(ctx, digest, contents); err != nil {
		t.Fatalf("repairing Put() error = %v", err)
	}
	repaired, err := store.Read(ctx, digest)
	if err != nil || string(repaired) != string(contents) {
		t.Fatalf("repaired Read() = %q, %v", repaired, err)
	}
}

func TestWorkspaceMaterializesVerifiedFilesIntoFreshJobDirectory(t *testing.T) {
	stateDir := t.TempDir()
	contentDir, err := paths.ContentStoreDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	content, err := New(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspaceStore(stateDir, content)
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("#!/bin/sh\n")
	second := []byte("echo hello\n")
	for _, contents := range [][]byte{first, second} {
		if err := content.Put(context.Background(), snapshot.Sum(contents), contents); err != nil {
			t.Fatal(err)
		}
	}
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{
			{Path: "README.md", Mode: 0o644, Size: 0},
			{Path: "cmd/run.sh", Mode: 0o755, Size: int64(len(first) + len(second)), Chunks: []snapshot.Chunk{
				{Digest: snapshot.Sum(first), Size: uint32(len(first))},
				{Digest: snapshot.Sum(second), Size: uint32(len(second))},
			}},
		},
		TotalBytes: int64(len(first) + len(second)),
	}
	id := job.ID("9ea4b467-f68b-48f5-824a-51be1f57fa59")
	workingDirectory, err := workspace.Materialize(context.Background(), id, manifest, "cmd")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(workingDirectory, "run.sh"))
	want := append(append([]byte(nil), first...), second...)
	if err != nil || string(contents) != string(want) {
		t.Fatalf("materialized file = %q, %v", contents, err)
	}
	info, err := os.Stat(filepath.Join(workingDirectory, "run.sh"))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("materialized mode = %v, %v", info.Mode(), err)
	}
	if _, err := workspace.Materialize(context.Background(), id, manifest, "cmd"); !errors.Is(err, ErrWorkspaceExists) {
		t.Fatalf("duplicate workspace error = %v", err)
	}
	if _, err := workspace.Materialize(
		context.Background(), job.ID("3ca0a75c-a82d-49fc-bb51-ed657ce4f021"), manifest, "../escape",
	); !errors.Is(err, snapshot.ErrUnsafePath) {
		t.Fatalf("unsafe working directory error = %v", err)
	}
}

func TestWorkspaceRefusesMissingChunkWithoutLeavingFinalDirectory(t *testing.T) {
	stateDir := t.TempDir()
	contentDir, _ := paths.ContentStoreDir(stateDir)
	content, _ := New(contentDir)
	workspace, _ := NewWorkspaceStore(stateDir, content)
	missing := snapshot.Sum([]byte("missing"))
	manifest := snapshot.Manifest{
		Version:    snapshot.ManifestVersion,
		Files:      []snapshot.File{{Path: "file", Mode: 0o644, Size: 7, Chunks: []snapshot.Chunk{{Digest: missing, Size: 7}}}},
		TotalBytes: 7,
	}
	id := job.ID("6ca59a42-10fb-4f73-802b-aaf800a9ea21")
	if _, err := workspace.Materialize(context.Background(), id, manifest, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Materialize() error = %v", err)
	}
	target, _ := paths.JobWorkspacePath(stateDir, id)
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial target exists: %v", err)
	}
}
