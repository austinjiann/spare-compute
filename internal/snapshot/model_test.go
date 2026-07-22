package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManifestIdentityIsCanonicalAndRejectsUnsafePaths(t *testing.T) {
	chunk := []byte("hello")
	manifest := Manifest{
		Version: ManifestVersion,
		Files: []File{{
			Path: "cmd/app/main.go", Mode: 0o644, Size: int64(len(chunk)),
			Chunks: []Chunk{{Digest: Sum(chunk), Size: uint32(len(chunk))}},
		}},
		TotalBytes: int64(len(chunk)),
	}
	first, err := manifest.ID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := manifest.Clone().ID()
	if err != nil || first != second || !first.Valid() {
		t.Fatalf("IDs = %q, %q; error = %v", first, second, err)
	}

	invalid := manifest.Clone()
	invalid.Files[0].Path = "../secret"
	if !errors.Is(invalid.Validate(), ErrUnsafePath) {
		t.Fatalf("unsafe path error = %v", invalid.Validate())
	}
	invalid = manifest.Clone()
	invalid.Files[0].Chunks[0].Size++
	if !errors.Is(invalid.Validate(), ErrInvalidManifest) {
		t.Fatalf("wrong size error = %v", invalid.Validate())
	}
	for _, name := range []string{"CON", "folder/AUX.txt", "trailing.", "has:colon", "a\\b"} {
		if !errors.Is(ValidatePath(name), ErrUnsafePath) {
			t.Fatalf("ValidatePath(%q) was accepted", name)
		}
	}
	collision := manifest.Clone()
	collision.Files = append(collision.Files, collision.Files[0])
	collision.Files[0].Path = "CMD/app/main.go"
	collision.Files[1].Path = "cmd/app/main.go"
	collision.TotalBytes *= 2
	if !errors.Is(collision.Validate(), ErrInvalidManifest) {
		t.Fatalf("portable path collision error = %v", collision.Validate())
	}
}

func TestContentDefinedChunkingReusesMostChunksAfterLocalInsertion(t *testing.T) {
	original := deterministicBytes(6 << 20)
	changed := make([]byte, 0, len(original)+(8<<10))
	changed = append(changed, original[:2<<20]...)
	changed = append(changed, bytes.Repeat([]byte("inserted"), 1<<10)...)
	changed = append(changed, original[2<<20:]...)

	first := chunkDigests(t, original)
	second := chunkDigests(t, changed)
	available := make(map[Digest]struct{}, len(first))
	for _, digest := range first {
		available[digest] = struct{}{}
	}
	reused := 0
	for _, digest := range second {
		if _, ok := available[digest]; ok {
			reused++
		}
	}
	if len(first) < 8 || reused < len(first)/2 {
		t.Fatalf("chunks before = %d, after = %d, reused = %d", len(first), len(second), reused)
	}
}

func TestResolveProjectPrefersGitRootOverNestedPackageMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "packages", "app", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, filepath.Join(root, "packages", "app", "package.json"), []byte("{}"), 0o644)
	resolved, subdirectory, err := ResolveProject(nested)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantRoot || subdirectory != "packages/app/src" {
		t.Fatalf("ResolveProject() = %q, %q", resolved, subdirectory)
	}
}

func TestResolveProjectAcceptsSymlinkToSelectedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a test symlink requires Windows developer mode")
	}
	root := t.TempDir()
	writeSnapshotTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644)
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	resolved, subdirectory, err := ResolveProject(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want || subdirectory != "" {
		t.Fatalf("ResolveProject() = %q, %q; want %q, empty", resolved, subdirectory, want)
	}
}

func TestBuildResolvesProjectAppliesNestedIgnoreRulesAndStoresChunks(t *testing.T) {
	root := t.TempDir()
	writeSnapshotTestFile(t, filepath.Join(root, ".git", "config"), []byte("ignored"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, ".gitignore"), []byte("*.tmp\nbuild/\n!keep.tmp\n!.git/\n!.computehop-results/\n"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, ".computehopignore"), []byte("!special.tmp\nprivate.txt\n"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "keep.tmp"), []byte("keep"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "special.tmp"), []byte("keep"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "private.txt"), []byte("drop"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "drop.tmp"), []byte("drop"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "build", "binary"), []byte("drop"), 0o755)
	writeSnapshotTestFile(t, filepath.Join(root, "build", ".gitignore"), bytes.Repeat([]byte("x"), maximumIgnoreBytes+1), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, ".computehop-results", "secret"), []byte("drop"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "cmd", ".gitignore"), []byte("generated.go\n"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "cmd", "generated.go"), []byte("drop"), 0o644)
	writeSnapshotTestFile(t, filepath.Join(root, "cmd", "app", "main.go"), []byte("package main\n"), 0o755)

	store := newMemoryStore()
	result, err := Build(context.Background(), filepath.Join(root, "cmd", "app"), store)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Root != wantRoot || result.WorkingSubdirectory != "cmd/app" {
		t.Fatalf("result root = %q, subdirectory = %q", result.Root, result.WorkingSubdirectory)
	}
	want := []string{".computehopignore", ".gitignore", "cmd/.gitignore", "cmd/app/main.go", "go.mod", "keep.tmp", "special.tmp"}
	if len(result.Manifest.Files) != len(want) {
		t.Fatalf("files = %#v", result.Manifest.Files)
	}
	for index, name := range want {
		if result.Manifest.Files[index].Path != name {
			t.Fatalf("file %d = %q, want %q", index, result.Manifest.Files[index].Path, name)
		}
	}
	if runtime.GOOS != "windows" && result.Manifest.Files[3].Mode != 0o755 {
		t.Fatalf("executable mode = %#o", result.Manifest.Files[3].Mode)
	}
	for _, digest := range result.Manifest.Digests() {
		if _, ok := store.contents[digest]; !ok {
			t.Fatalf("chunk %s was not stored", digest)
		}
	}
}

func TestIgnoreRulesRejectOversizedFilesAndTreatEscapedBangLiterally(t *testing.T) {
	if _, err := parseIgnoreRules(bytes.NewReader(bytes.Repeat([]byte("x"), maximumIgnoreBytes+1)), ""); err == nil {
		t.Fatal("oversized ignore file was accepted")
	}
	rules, err := parseIgnoreRules(bytes.NewBufferString("\\!literal\n!included\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	matcher := ignoreMatcher{rules: rules}
	if !matcher.ignored("!literal", false) {
		t.Fatal("escaped leading bang was interpreted as negation")
	}
	if matcher.ignored("included", false) {
		t.Fatal("unescaped leading bang was not interpreted as negation")
	}
}

func TestBuildRejectsSymlinksInsideProject(t *testing.T) {
	root := t.TempDir()
	writeSnapshotTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644)
	if err := os.Symlink("go.mod", filepath.Join(root, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating a test symlink requires Windows developer mode: %v", err)
		}
		t.Fatal(err)
	}
	_, err := Build(context.Background(), root, newMemoryStore())
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("symlink error = %v", err)
	}
}

func chunkDigests(t *testing.T, contents []byte) []Digest {
	t.Helper()
	var result []Digest
	if err := ChunkReader(context.Background(), bytes.NewReader(contents), func(chunk []byte) error {
		if len(chunk) == 0 || len(chunk) > MaximumChunkBytes {
			t.Fatalf("chunk size = %d", len(chunk))
		}
		result = append(result, Sum(chunk))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func deterministicBytes(length int) []byte {
	result := make([]byte, length)
	value := uint64(0x123456789abcdef0)
	for index := range result {
		value ^= value << 13
		value ^= value >> 7
		value ^= value << 17
		result[index] = byte(value)
	}
	return result
}

type memoryStore struct {
	contents map[Digest][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{contents: make(map[Digest][]byte)}
}

func (store *memoryStore) Put(_ context.Context, digest Digest, contents []byte) error {
	if Sum(contents) != digest {
		return ErrInvalidDigest
	}
	store.contents[digest] = append([]byte(nil), contents...)
	return nil
}

func writeSnapshotTestFile(t *testing.T, name string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, contents, mode); err != nil {
		t.Fatal(err)
	}
}
