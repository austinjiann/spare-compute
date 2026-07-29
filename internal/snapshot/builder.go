package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maximumStableFileAttempts = 3

var (
	ErrInvalidRoot      = errors.New("invalid snapshot root")
	ErrUnsupportedEntry = errors.New("unsupported project entry")
	ErrProjectChanged   = errors.New("project changed while snapshotting")
)

// ChunkStore persists verified local content for later incremental transfer.
type ChunkStore interface {
	Put(context.Context, Digest, []byte) error
}

// Builder snapshots projects into one persistent local chunk store.
type Builder struct {
	store ChunkStore
}

// NewBuilder constructs a reusable project snapshotter.
func NewBuilder(store ChunkStore) (*Builder, error) {
	if store == nil {
		return nil, ErrInvalidRoot
	}
	return &Builder{store: store}, nil
}

// Build resolves and snapshots workingDirectory.
func (builder *Builder) Build(ctx context.Context, workingDirectory string) (Result, error) {
	if builder == nil || builder.store == nil {
		return Result{}, ErrInvalidRoot
	}
	return Build(ctx, workingDirectory, builder.store)
}

// Result binds a manifest to the resolved project and command directory.
type Result struct {
	Root                string
	WorkingSubdirectory string
	Manifest            Manifest
}

// ResolveProject finds the nearest project marker above workingDirectory.
func ResolveProject(workingDirectory string) (string, string, error) {
	absolute, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", "", ErrInvalidRoot
	}
	root := absolute
	nearestMarker := ""
	for current := absolute; ; current = filepath.Dir(current) {
		if pathExists(filepath.Join(current, ".git")) {
			root = current
			break
		}
		if nearestMarker == "" && projectMarker(current) {
			nearestMarker = current
		}
		parent := filepath.Dir(current)
		if parent == current {
			if nearestMarker != "" {
				root = nearestMarker
			}
			break
		}
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", "", err
	}
	if relative == "." {
		relative = ""
	} else {
		relative = filepath.ToSlash(relative)
		if err := ValidatePath(relative); err != nil {
			return "", "", err
		}
	}
	return root, relative, nil
}

func projectMarker(directory string) bool {
	for _, name := range []string{"computehop.toml", "Cargo.toml", "go.mod", "package.json"} {
		if pathExists(filepath.Join(directory, name)) {
			return true
		}
	}
	return false
}

func pathExists(filename string) bool {
	_, err := os.Lstat(filename)
	return err == nil
}

// Build creates a stable manifest and writes each unique chunk to store.
func Build(ctx context.Context, workingDirectory string, store ChunkStore) (Result, error) {
	if store == nil {
		return Result{}, ErrInvalidRoot
	}
	root, subdirectory, err := ResolveProject(workingDirectory)
	if err != nil {
		return Result{}, err
	}
	matcher, err := loadIgnoreMatcher(root)
	if err != nil {
		return Result{}, err
	}
	manifest := Manifest{Version: ManifestVersion}
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if matcher.ignored(relative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %s", ErrUnsupportedEntry, relative)
		}
		file, err := buildStableFile(ctx, current, relative, store)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, file)
		manifest.TotalBytes += file.Size
		if len(manifest.Files) > MaximumFiles || manifest.TotalBytes > MaximumSnapshotBytes {
			return ErrInvalidManifest
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	sort.Slice(manifest.Files, func(left, right int) bool {
		return manifest.Files[left].Path < manifest.Files[right].Path
	})
	if _, err := manifest.CanonicalBytes(); err != nil {
		return Result{}, err
	}
	return Result{Root: root, WorkingSubdirectory: subdirectory, Manifest: manifest}, nil
}

func buildStableFile(
	ctx context.Context,
	filename string,
	relative string,
	store ChunkStore,
) (File, error) {
	var err error
	for range maximumStableFileAttempts {
		var result File
		result, err = buildFile(ctx, filename, relative, store)
		if !errors.Is(err, ErrProjectChanged) {
			return result, err
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return File{}, contextErr
		}
	}
	return File{}, err
}

func buildFile(
	ctx context.Context,
	filename string,
	relative string,
	store ChunkStore,
) (File, error) {
	if err := ValidatePath(relative); err != nil {
		return File{}, err
	}
	opened, err := os.Open(filename)
	if err != nil {
		return File{}, err
	}
	defer opened.Close()
	before, err := opened.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return File{}, ErrUnsupportedEntry
	}
	mode := uint32(0o644)
	if before.Mode()&0o111 != 0 {
		mode = 0o755
	}
	result := File{Path: relative, Mode: mode, Size: before.Size()}
	err = ChunkReader(ctx, opened, func(contents []byte) error {
		digest := Sum(contents)
		if err := store.Put(ctx, digest, contents); err != nil {
			return err
		}
		result.Chunks = append(result.Chunks, Chunk{Digest: digest, Size: uint32(len(contents))})
		return nil
	})
	if err != nil {
		return File{}, err
	}
	after, err := opened.Stat()
	if err != nil {
		return File{}, err
	}
	if before.Size() != after.Size() || !sameModificationTime(before.ModTime(), after.ModTime()) {
		return File{}, fmt.Errorf("%w: %s", ErrProjectChanged, relative)
	}
	var chunkBytes int64
	for _, chunk := range result.Chunks {
		chunkBytes += int64(chunk.Size)
	}
	if chunkBytes != result.Size {
		return File{}, fmt.Errorf("%w: %s", ErrProjectChanged, relative)
	}
	return result, nil
}

func sameModificationTime(left, right time.Time) bool {
	return left.Equal(right)
}
