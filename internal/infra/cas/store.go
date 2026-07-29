// Package cas stores verified content-addressed snapshot chunks.
package cas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

var (
	ErrInvalidStore     = errors.New("invalid content store")
	ErrNotFound         = errors.New("content chunk not found")
	ErrCorrupt          = errors.New("content chunk failed verification")
	ErrReservationLimit = contentcache.ErrReservationLimit
)

// Store is an owner-local immutable chunk store.
type Store struct {
	root         string
	index        contentcache.Repository
	maximumBytes int64
	now          func() time.Time

	accessMu      sync.Mutex
	activeUses    int
	operationPins map[snapshot.Digest]struct{}
	transientPins map[snapshot.Digest]int
	reservations  map[snapshot.Digest]reservation
	enforceMu     sync.Mutex
}

// New validates and creates an owner-only store root.
func New(root string) (*Store, error) {
	return newStore(root)
}

func newStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, ErrInvalidStore
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create content store: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("restrict content store: %w", err)
	}
	return &Store{root: filepath.Clean(root)}, nil
}

// Put verifies and atomically stores one bounded chunk.
func (store *Store) Put(ctx context.Context, digest snapshot.Digest, contents []byte) error {
	if store == nil || !digest.Valid() || len(contents) == 0 || len(contents) > snapshot.MaximumChunkBytes ||
		snapshot.Sum(contents) != digest {
		return ErrCorrupt
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release := store.pinAccess(digest)
	defer release()
	filename, err := store.chunkPath(digest)
	if err != nil {
		return err
	}
	present, hasErr := store.Has(ctx, digest)
	if present {
		return nil
	}
	if hasErr != nil {
		if !errors.Is(hasErr, ErrCorrupt) {
			return hasErr
		}
		// Chunks are a recoverable cache. Remove a corrupt entry so a verified
		// upload can repair it without requiring manual state-directory surgery.
		if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove corrupt content chunk: %w", err)
		}
		if store.index != nil {
			if err := store.index.Delete(ctx, digest); err != nil {
				return err
			}
		}
	}
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create chunk directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".incoming-*")
	if err != nil {
		return fmt.Errorf("create temporary chunk: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	writeErr := func() error {
		if err := temporary.Chmod(0o600); err != nil {
			return err
		}
		if _, err := temporary.Write(contents); err != nil {
			return err
		}
		if err := temporary.Sync(); err != nil {
			return err
		}
		return temporary.Close()
	}()
	if writeErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("write content chunk: %w", writeErr)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		if present, hasErr := store.Has(ctx, digest); present && hasErr == nil {
			return nil
		}
		return fmt.Errorf("commit content chunk: %w", err)
	}
	if store.index != nil {
		if err := store.index.Record(ctx, contentcache.Entry{
			Digest: digest, Size: int64(len(contents)), LastAccessed: store.now().UTC(),
		}); err != nil {
			return err
		}
		if err := store.Enforce(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Has verifies whether one chunk is already present.
func (store *Store) Has(ctx context.Context, digest snapshot.Digest) (bool, error) {
	_, err := store.Read(ctx, digest)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// Missing returns unique absent digests in lexical order.
func (store *Store) Missing(ctx context.Context, digests []snapshot.Digest) ([]snapshot.Digest, error) {
	unique := make(map[snapshot.Digest]struct{}, len(digests))
	for _, digest := range digests {
		if !digest.Valid() {
			return nil, snapshot.ErrInvalidDigest
		}
		unique[digest] = struct{}{}
	}
	ordered := make([]snapshot.Digest, 0, len(unique))
	for digest := range unique {
		ordered = append(ordered, digest)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	missing := make([]snapshot.Digest, 0)
	for _, digest := range ordered {
		present, err := store.Has(ctx, digest)
		if errors.Is(err, ErrCorrupt) {
			missing = append(missing, digest)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !present {
			missing = append(missing, digest)
		}
	}
	return missing, nil
}

// Read loads and re-verifies one bounded chunk.
func (store *Store) Read(ctx context.Context, digest snapshot.Digest) ([]byte, error) {
	if store == nil || !digest.Valid() {
		return nil, snapshot.ErrInvalidDigest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release := store.pinAccess(digest)
	defer release()
	filename, err := store.chunkPath(digest)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		if store.index != nil {
			if deleteErr := store.index.Delete(ctx, digest); deleteErr != nil {
				return nil, deleteErr
			}
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open content chunk: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect content chunk: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > snapshot.MaximumChunkBytes {
		return nil, ErrCorrupt
	}
	contents, err := io.ReadAll(io.LimitReader(file, snapshot.MaximumChunkBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read content chunk: %w", err)
	}
	if len(contents) != int(info.Size()) || snapshot.Sum(contents) != digest {
		return nil, ErrCorrupt
	}
	if store.index != nil {
		if err := store.index.Record(ctx, contentcache.Entry{
			Digest: digest, Size: info.Size(), LastAccessed: store.now().UTC(),
		}); err != nil {
			return nil, err
		}
	}
	return contents, nil
}

func (store *Store) chunkPath(digest snapshot.Digest) (string, error) {
	if store == nil || store.root == "" || !digest.Valid() {
		return "", ErrInvalidStore
	}
	value := string(digest)
	return filepath.Join(store.root, "chunks", value[:2], value), nil
}
