package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/austinjiann/spare-compute/internal/portablepath"
)

var ErrDeclaredOutputMissing = errors.New("declared output is missing")

// BuildDeclared snapshots exact output files or directories relative to root.
// Ignore files do not apply: an explicit output declaration is authoritative.
func BuildDeclared(
	ctx context.Context,
	root string,
	declarations []string,
	store ChunkStore,
) (Manifest, error) {
	if store == nil {
		return Manifest{}, ErrInvalidRoot
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return Manifest{}, ErrInvalidRoot
	}
	files := make(map[string]File)
	portablePaths := make(map[string]string)
	for _, declaration := range declarations {
		if err := ValidatePath(declaration); err != nil || reservedTransferPath(declaration) {
			return Manifest{}, ErrUnsafePath
		}
		selected := filepath.Join(absolute, filepath.FromSlash(declaration))
		selectedInfo, err := os.Lstat(selected)
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, fmt.Errorf("%w: %s", ErrDeclaredOutputMissing, declaration)
		}
		if err != nil {
			return Manifest{}, err
		}
		if selectedInfo.Mode()&os.ModeSymlink != 0 || !selectedInfo.IsDir() && !selectedInfo.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("%w: %s", ErrUnsupportedEntry, declaration)
		}
		if selectedInfo.IsDir() {
			err = filepath.WalkDir(selected, func(current string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if current == selected || entry.IsDir() {
					return nil
				}
				return addDeclaredFile(ctx, absolute, current, entry, store, files, portablePaths)
			})
		} else {
			err = addDeclaredPath(ctx, absolute, selected, store, files, portablePaths)
		}
		if err != nil {
			return Manifest{}, err
		}
	}
	manifest := Manifest{Version: ManifestVersion, Files: make([]File, 0, len(files))}
	for _, file := range files {
		manifest.Files = append(manifest.Files, file)
		manifest.TotalBytes += file.Size
	}
	sort.Slice(manifest.Files, func(left, right int) bool {
		return manifest.Files[left].Path < manifest.Files[right].Path
	})
	if _, err := manifest.CanonicalBytes(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func addDeclaredFile(
	ctx context.Context,
	root string,
	filename string,
	entry fs.DirEntry,
	store ChunkStore,
	files map[string]File,
	portablePaths map[string]string,
) error {
	if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
		return fmt.Errorf("%w: %s", ErrUnsupportedEntry, filename)
	}
	return addDeclaredPath(ctx, root, filename, store, files, portablePaths)
}

func addDeclaredPath(
	ctx context.Context,
	root string,
	filename string,
	store ChunkStore,
	files map[string]File,
	portablePaths map[string]string,
) error {
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	if err := ValidatePath(relative); err != nil || reservedTransferPath(relative) {
		return ErrUnsafePath
	}
	key := portablepath.Key(relative)
	if existing, ok := portablePaths[key]; ok {
		if existing == relative {
			return nil
		}
		return fmt.Errorf("%w: path collision for %q", ErrInvalidManifest, relative)
	}
	file, err := buildStableFile(ctx, filename, relative, store)
	if err != nil {
		return err
	}
	if len(files) >= MaximumFiles {
		return ErrInvalidManifest
	}
	files[relative] = file
	portablePaths[key] = relative
	return nil
}

func reservedTransferPath(value string) bool {
	for _, segment := range strings.Split(portablepath.Key(value), "/") {
		if segment == ".git" || segment == ".computehop-results" || segment == ".computehop-conflicts" {
			return true
		}
	}
	return false
}
