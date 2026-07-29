package cas

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

const (
	defaultReservationLifetime = 10 * time.Minute
	maximumReservations        = 64
)

// Config enables a persistent, bounded content store.
type Config struct {
	Root         string
	Index        contentcache.Repository
	MaximumBytes int64
	Now          func() time.Time
}

type reservation struct {
	digests   map[snapshot.Digest]struct{}
	expiresAt time.Time
}

// NewManaged opens, reconciles, and prunes a quota-managed content store.
func NewManaged(ctx context.Context, config Config) (*Store, error) {
	if config.Index == nil || config.Now == nil {
		return nil, ErrInvalidStore
	}
	if err := contentcache.ValidateMaximumBytes(config.MaximumBytes); err != nil {
		return nil, err
	}
	store, err := newStore(config.Root)
	if err != nil {
		return nil, err
	}
	store.index = config.Index
	store.maximumBytes = config.MaximumBytes
	store.now = config.Now
	store.operationPins = make(map[snapshot.Digest]struct{})
	store.transientPins = make(map[snapshot.Digest]int)
	store.reservations = make(map[snapshot.Digest]reservation)
	if err := store.reconcile(ctx); err != nil {
		return nil, err
	}
	if err := store.Enforce(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// BeginUse keeps every chunk touched by a multi-step operation in the cache
// until the returned idempotent release function is called.
func (store *Store) BeginUse() func() {
	if store == nil || store.index == nil {
		return func() {}
	}
	store.accessMu.Lock()
	store.expireReservationsLocked(store.now().UTC())
	store.activeUses++
	store.accessMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			store.accessMu.Lock()
			store.activeUses--
			last := store.activeUses == 0
			if last {
				clear(store.operationPins)
			}
			store.accessMu.Unlock()
			if last {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = store.Enforce(ctx)
			}
		})
	}
}

// Reserve pins a bounded batch of a manifest's chunks while an upload spans
// multiple RPCs. Repeating the same manifest extends and augments its lease.
func (store *Store) Reserve(
	ctx context.Context,
	manifestID snapshot.Digest,
	digests []snapshot.Digest,
) error {
	if store == nil || store.index == nil || !manifestID.Valid() || len(digests) == 0 ||
		len(digests) > snapshot.MaximumChunks {
		return ErrInvalidStore
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unique := make(map[snapshot.Digest]struct{}, len(digests))
	for _, digest := range digests {
		if !digest.Valid() {
			return snapshot.ErrInvalidDigest
		}
		unique[digest] = struct{}{}
	}
	now := store.now().UTC()
	store.accessMu.Lock()
	store.expireReservationsLocked(now)
	current, exists := store.reservations[manifestID]
	if !exists {
		if len(store.reservations) >= maximumReservations {
			store.accessMu.Unlock()
			return ErrReservationLimit
		}
		current = reservation{digests: make(map[snapshot.Digest]struct{})}
	}
	additions := 0
	for digest := range unique {
		if _, present := current.digests[digest]; !present {
			additions++
		}
	}
	if len(current.digests)+additions > snapshot.MaximumChunks {
		store.accessMu.Unlock()
		return ErrInvalidStore
	}
	for digest := range unique {
		current.digests[digest] = struct{}{}
	}
	current.expiresAt = now.Add(defaultReservationLifetime)
	store.reservations[manifestID] = current
	store.accessMu.Unlock()
	return store.Enforce(ctx)
}

// ReleaseReservation allows unused transfer chunks to become LRU candidates.
func (store *Store) ReleaseReservation(manifestID snapshot.Digest) {
	if store == nil || store.index == nil || !manifestID.Valid() {
		return
	}
	store.accessMu.Lock()
	delete(store.reservations, manifestID)
	store.accessMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = store.Enforce(ctx)
}

// Stats returns durable cache usage.
func (store *Store) Stats(ctx context.Context) (contentcache.Stats, error) {
	if store == nil || store.index == nil {
		return contentcache.Stats{}, ErrInvalidStore
	}
	return store.index.Stats(ctx)
}

// Enforce evicts least-recently-used chunks until the configured hard limit is
// met. Artifact references, active operations, and incoming snapshots are safe.
func (store *Store) Enforce(ctx context.Context) error {
	if store == nil || store.index == nil {
		return nil
	}
	store.enforceMu.Lock()
	defer store.enforceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	stats, err := store.index.Stats(ctx)
	if err != nil || stats.Bytes <= store.maximumBytes {
		return err
	}
	candidates, err := store.index.EvictionCandidates(ctx)
	if err != nil {
		return err
	}
	currentBytes := stats.Bytes
	pinnedBytes := stats.ProtectedBytes
	for _, candidate := range candidates {
		if currentBytes <= store.maximumBytes {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		store.accessMu.Lock()
		store.expireReservationsLocked(store.now().UTC())
		if store.pinnedLocked(candidate.Digest) {
			pinnedBytes += candidate.Size
			store.accessMu.Unlock()
			continue
		}
		filename, pathErr := store.chunkPath(candidate.Digest)
		if pathErr == nil {
			pathErr = os.Remove(filename)
		}
		store.accessMu.Unlock()
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return fmt.Errorf("evict content cache chunk: %w", pathErr)
		}
		if err := store.index.Delete(ctx, candidate.Digest); err != nil {
			return err
		}
		currentBytes -= candidate.Size
	}
	remaining, err := store.index.Stats(ctx)
	if err != nil {
		return err
	}
	if remaining.Bytes > store.maximumBytes {
		return &contentcache.QuotaError{
			MaximumBytes: store.maximumBytes,
			CurrentBytes: remaining.Bytes,
			PinnedBytes:  min(pinnedBytes, remaining.Bytes),
		}
	}
	return nil
}

func (store *Store) pinAccess(digest snapshot.Digest) func() {
	if store == nil || store.index == nil {
		return func() {}
	}
	store.accessMu.Lock()
	store.expireReservationsLocked(store.now().UTC())
	store.transientPins[digest]++
	if store.activeUses > 0 {
		store.operationPins[digest] = struct{}{}
	}
	store.accessMu.Unlock()
	return func() {
		store.accessMu.Lock()
		store.transientPins[digest]--
		if store.transientPins[digest] == 0 {
			delete(store.transientPins, digest)
		}
		store.accessMu.Unlock()
	}
}

func (store *Store) pinnedLocked(digest snapshot.Digest) bool {
	if store.transientPins[digest] > 0 {
		return true
	}
	if _, exists := store.operationPins[digest]; exists {
		return true
	}
	for _, current := range store.reservations {
		if _, exists := current.digests[digest]; exists {
			return true
		}
	}
	return false
}

func (store *Store) expireReservationsLocked(now time.Time) {
	for id, current := range store.reservations {
		if !current.expiresAt.After(now) {
			delete(store.reservations, id)
		}
	}
}

func (store *Store) reconcile(ctx context.Context) error {
	chunks := filepath.Join(store.root, "chunks")
	entries := make([]contentcache.Entry, 0)
	err := filepath.WalkDir(chunks, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		digest, parseErr := snapshot.ParseDigest(name)
		expected, pathErr := store.chunkPath(digest)
		info, infoErr := entry.Info()
		valid := parseErr == nil && pathErr == nil && filepath.Clean(path) == expected &&
			infoErr == nil && info.Mode().IsRegular() && info.Size() > 0 &&
			info.Size() <= snapshot.MaximumChunkBytes
		if !valid {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove invalid content cache entry: %w", removeErr)
			}
			return nil
		}
		entries = append(entries, contentcache.Entry{
			Digest: digest, Size: info.Size(), LastAccessed: info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("scan content cache: %w", err)
	}
	if err := store.index.Reconcile(ctx, entries); err != nil {
		return err
	}
	return removeEmptyDirectories(chunks)
}

func removeEmptyDirectories(root string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		// Windows and Unix expose different non-empty directory errors; shard
		// directories are harmless, so cleanup is deliberately best effort.
		_ = os.Remove(directories[index])
	}
	return nil
}
