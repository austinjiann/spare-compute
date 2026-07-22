package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

// ArtifactManager collects, verifies, reads, and conflict-safely restores
// immutable declared outputs using the shared content store.
type ArtifactManager struct {
	content    *Store
	repository artifact.Repository
	now        func() time.Time
}

// NewArtifactManager constructs the artifact boundary shared by the runner and
// remote-control application services.
func NewArtifactManager(
	content *Store,
	repository artifact.Repository,
	now func() time.Time,
) (*ArtifactManager, error) {
	if content == nil || repository == nil || now == nil {
		return nil, ErrInvalidStore
	}
	return &ArtifactManager{content: content, repository: repository, now: now}, nil
}

// Collect snapshots exact declared outputs and durably publishes their manifest.
func (manager *ArtifactManager) Collect(ctx context.Context, value job.Job) (artifact.Bundle, error) {
	if manager == nil || manager.content == nil || manager.repository == nil || manager.now == nil ||
		value.Validate() != nil || len(value.Spec.Outputs) == 0 {
		return artifact.Bundle{}, artifact.ErrInvalidBundle
	}
	manifest, err := snapshot.BuildDeclared(ctx, value.Spec.WorkingDirectory, value.Spec.Outputs, manager.content)
	if err != nil {
		return artifact.Bundle{}, err
	}
	bundle := artifact.Bundle{JobID: value.ID, Manifest: manifest, CollectedAt: manager.now().UTC()}
	if err := manager.repository.Save(ctx, bundle); err != nil {
		return artifact.Bundle{}, err
	}
	return bundle.Clone(), nil
}

// Get returns one revalidated durable bundle.
func (manager *ArtifactManager) Get(ctx context.Context, id job.ID) (artifact.Bundle, error) {
	if manager == nil || manager.repository == nil {
		return artifact.Bundle{}, ErrInvalidStore
	}
	return manager.repository.Get(ctx, id)
}

// ReadJobChunk returns only content referenced by the selected job's artifact
// manifest, preventing the content cache from becoming a general hash oracle.
func (manager *ArtifactManager) ReadJobChunk(
	ctx context.Context,
	id job.ID,
	digest snapshot.Digest,
) ([]byte, error) {
	if manager == nil || manager.content == nil || !digest.Valid() {
		return nil, ErrInvalidStore
	}
	bundle, err := manager.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	found := false
	for _, candidate := range bundle.Manifest.Digests() {
		if candidate == digest {
			found = true
			break
		}
	}
	if !found {
		return nil, artifact.ErrNotFound
	}
	return manager.content.Read(ctx, digest)
}

// Restore reconstructs a complete bundle in staging, then atomically places
// each file without overwriting existing local content.
func (manager *ArtifactManager) Restore(
	ctx context.Context,
	bundle artifact.Bundle,
	destination string,
) (artifact.RestoreResult, error) {
	if manager == nil || manager.content == nil || bundle.Validate() != nil ||
		!filepath.IsAbs(destination) {
		return artifact.RestoreResult{}, artifact.ErrInvalidDestination
	}
	destination = filepath.Clean(destination)
	if filepath.Dir(destination) == destination {
		return artifact.RestoreResult{}, artifact.ErrInvalidDestination
	}
	missing, err := manager.content.Missing(ctx, bundle.Manifest.Digests())
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	if len(missing) != 0 {
		return artifact.RestoreResult{}, fmt.Errorf("%w: %d artifact chunks", ErrNotFound, len(missing))
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return artifact.RestoreResult{}, err
	}
	staging, err := os.MkdirTemp(parent, ".computehop-restore-*")
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	defer os.RemoveAll(staging)
	for _, file := range bundle.Manifest.Files {
		if err := ctx.Err(); err != nil {
			return artifact.RestoreResult{}, err
		}
		target := filepath.Join(staging, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return artifact.RestoreResult{}, err
		}
		if err := materializeFile(ctx, manager.content, target, file); err != nil {
			return artifact.RestoreResult{}, err
		}
	}
	if err := ensureRestoreRoot(destination); err != nil {
		return artifact.RestoreResult{}, artifact.ErrInvalidDestination
	}
	result := artifact.RestoreResult{Destination: destination}
	for _, file := range bundle.Manifest.Files {
		source := filepath.Join(staging, filepath.FromSlash(file.Path))
		target := filepath.Join(destination, filepath.FromSlash(file.Path))
		parentReady, err := ensureRestoreDirectory(destination, filepath.Dir(filepath.FromSlash(file.Path)))
		if err != nil {
			return artifact.RestoreResult{}, err
		}
		placed := false
		if parentReady {
			placed, err = placeWithoutOverwrite(source, target)
			if err != nil {
				return artifact.RestoreResult{}, err
			}
		}
		if placed {
			result.Restored = append(result.Restored, file.Path)
			continue
		}
		conflict, err := placeConflict(source, destination, bundle.JobID, file.Path)
		if err != nil {
			return artifact.RestoreResult{}, err
		}
		result.Conflicts = append(result.Conflicts, conflict)
	}
	result = artifact.NormalizeResult(result)
	if err := result.Validate(); err != nil {
		return artifact.RestoreResult{}, err
	}
	return result, nil
}

func ensureRestoreRoot(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(root)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return artifact.ErrInvalidDestination
	}
	return nil
}

// ensureRestoreDirectory creates and verifies every descendant one component
// at a time. Existing symlinks are never followed; a regular file blocking the
// desired hierarchy is reported as a conflict candidate instead.
func ensureRestoreDirectory(root, relative string) (bool, error) {
	if relative == "." || relative == "" {
		return true, ensureRestoreRoot(root)
	}
	current := root
	for _, segment := range splitNativePath(relative) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return false, err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, artifact.ErrInvalidDestination
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

func splitNativePath(value string) []string {
	volume := filepath.VolumeName(value)
	value = strings.TrimPrefix(value, volume)
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == '/' || character == '\\'
	})
}

func placeWithoutOverwrite(source, target string) (bool, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(source, target); err != nil {
			if _, statErr := os.Lstat(target); statErr == nil {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode().IsRegular() {
		equal, err := filesEqual(source, target)
		if err != nil {
			return false, err
		}
		if equal {
			return true, os.Remove(source)
		}
	}
	return false, nil
}

func placeConflict(source, destination string, id job.ID, original string) (string, error) {
	identifier := string(id)
	for attempt := 1; attempt <= 100; attempt++ {
		segment := identifier
		if attempt > 1 {
			segment += "-" + strconv.Itoa(attempt)
		}
		relative := filepath.ToSlash(filepath.Join(".computehop-conflicts", segment, filepath.FromSlash(original)))
		target := filepath.Join(destination, filepath.FromSlash(relative))
		ready, err := ensureRestoreDirectory(destination, filepath.Dir(filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		if !ready {
			continue
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(source, target); err == nil {
				return relative, nil
			}
		} else if err == nil {
			equal, compareErr := filesEqual(source, target)
			if compareErr != nil {
				return "", compareErr
			}
			if equal {
				return relative, os.Remove(source)
			}
		} else {
			return "", err
		}
	}
	return "", artifact.ErrConflict
}

func filesEqual(leftName, rightName string) (bool, error) {
	leftInfo, err := os.Stat(leftName)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(rightName)
	if err != nil {
		return false, err
	}
	if !leftInfo.Mode().IsRegular() || !rightInfo.Mode().IsRegular() || leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	left, err := os.Open(leftName)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(rightName)
	if err != nil {
		return false, err
	}
	defer right.Close()
	leftBuffer := make([]byte, 64<<10)
	rightBuffer := make([]byte, 64<<10)
	for {
		leftRead, leftErr := io.ReadFull(left, leftBuffer)
		rightRead, rightErr := io.ReadFull(right, rightBuffer)
		if leftRead != rightRead || !bytes.Equal(leftBuffer[:leftRead], rightBuffer[:rightRead]) {
			return false, nil
		}
		if errors.Is(leftErr, io.EOF) || errors.Is(leftErr, io.ErrUnexpectedEOF) {
			return (errors.Is(rightErr, io.EOF) || errors.Is(rightErr, io.ErrUnexpectedEOF)) && leftRead == rightRead, nil
		}
		if leftErr != nil || rightErr != nil {
			return false, errors.Join(leftErr, rightErr)
		}
	}
}
