package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

var ErrWorkspaceExists = errors.New("job workspace already exists")

// WorkspaceStore materializes verified chunks into fresh job directories.
type WorkspaceStore struct {
	stateDir string
	content  *Store
}

// NewWorkspaceStore constructs a workspace materializer.
func NewWorkspaceStore(stateDir string, content *Store) (*WorkspaceStore, error) {
	if strings.TrimSpace(stateDir) == "" || !filepath.IsAbs(stateDir) || content == nil {
		return nil, ErrInvalidStore
	}
	return &WorkspaceStore{stateDir: filepath.Clean(stateDir), content: content}, nil
}

// Missing delegates verified preflight to the shared content cache.
func (store *WorkspaceStore) Missing(
	ctx context.Context,
	digests []snapshot.Digest,
) ([]snapshot.Digest, error) {
	if store == nil || store.content == nil {
		return nil, ErrInvalidStore
	}
	return store.content.Missing(ctx, digests)
}

// Put verifies one uploaded chunk before it enters the shared cache.
func (store *WorkspaceStore) Put(
	ctx context.Context,
	digest snapshot.Digest,
	contents []byte,
) error {
	if store == nil || store.content == nil {
		return ErrInvalidStore
	}
	return store.content.Put(ctx, digest, contents)
}

// BeginUse protects chunks touched while a complete snapshot is validated and
// materialized.
func (store *WorkspaceStore) BeginUse() func() {
	if store == nil || store.content == nil {
		return func() {}
	}
	return store.content.BeginUse()
}

// Reserve pins an incoming manifest across its batched preflight and upload RPCs.
func (store *WorkspaceStore) Reserve(
	ctx context.Context,
	manifestID snapshot.Digest,
	digests []snapshot.Digest,
) error {
	if store == nil || store.content == nil {
		return ErrInvalidStore
	}
	if store.content.index == nil {
		return nil
	}
	return store.content.Reserve(ctx, manifestID, digests)
}

// ReleaseReservation ends one incoming manifest lease.
func (store *WorkspaceStore) ReleaseReservation(manifestID snapshot.Digest) {
	if store != nil && store.content != nil {
		store.content.ReleaseReservation(manifestID)
	}
}

// Materialize reconstructs manifest into an isolated, owner-only workspace and
// returns the selected working directory.
func (store *WorkspaceStore) Materialize(
	ctx context.Context,
	id job.ID,
	manifest snapshot.Manifest,
	workingSubdirectory string,
) (string, error) {
	if store == nil || store.content == nil || !id.Valid() {
		return "", ErrInvalidStore
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	if workingSubdirectory != "" {
		if err := snapshot.ValidatePath(workingSubdirectory); err != nil {
			return "", err
		}
	}
	target, err := paths.JobWorkspacePath(store.stateDir, id)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		return "", ErrWorkspaceExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create job data directory: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".workspace-*")
	if err != nil {
		return "", fmt.Errorf("create temporary workspace: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return "", err
	}
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		destination := filepath.Join(staging, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return "", err
		}
		if err := materializeFile(ctx, store.content, destination, file); err != nil {
			return "", err
		}
	}
	if workingSubdirectory != "" {
		workingDirectory := filepath.Join(staging, filepath.FromSlash(workingSubdirectory))
		if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("commit job workspace: %w", err)
	}
	if workingSubdirectory == "" {
		return target, nil
	}
	return filepath.Join(target, filepath.FromSlash(workingSubdirectory)), nil
}

// RemoveWorkspace removes only the validated workspace for an unaccepted job.
func (store *WorkspaceStore) RemoveWorkspace(id job.ID) error {
	if store == nil || !id.Valid() {
		return ErrInvalidStore
	}
	target, err := paths.JobWorkspacePath(store.stateDir, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove job workspace: %w", err)
	}
	return nil
}

func materializeFile(ctx context.Context, content *Store, destination string, file snapshot.File) error {
	opened, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	var writeErr error
	for _, chunk := range file.Chunks {
		contents, err := content.Read(ctx, chunk.Digest)
		if err != nil {
			writeErr = err
			break
		}
		if len(contents) != int(chunk.Size) {
			writeErr = ErrCorrupt
			break
		}
		if _, err := io.Copy(opened, bytes.NewReader(contents)); err != nil {
			writeErr = err
			break
		}
	}
	closeErr := opened.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Chmod(destination, os.FileMode(file.Mode)); err != nil {
		return err
	}
	info, err := os.Stat(destination)
	if err != nil || info.Size() != file.Size {
		return ErrCorrupt
	}
	return nil
}
