// Package contentcache defines persistent metadata for the verified content cache.
package contentcache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/snapshot"
)

const (
	DefaultMaximumBytes int64 = 20 << 30
	MinimumMaximumBytes int64 = 1 << 20
	MaximumMaximumBytes int64 = 100 << 40
)

var (
	ErrInvalidEntry      = errors.New("invalid content cache entry")
	ErrInvalidQuota      = errors.New("invalid content cache quota")
	ErrQuotaExceeded     = errors.New("content cache quota exceeded")
	ErrReservationLimit  = errors.New("content cache reservation limit reached")
	ErrInvalidRepository = errors.New("invalid content cache repository")
)

// Entry records one verified on-disk chunk and its most recent successful use.
type Entry struct {
	Digest       snapshot.Digest
	Size         int64
	LastAccessed time.Time
}

// Validate checks an entry reconstructed from disk or persistence.
func (entry Entry) Validate() error {
	if !entry.Digest.Valid() || entry.Size <= 0 || entry.Size > snapshot.MaximumChunkBytes ||
		entry.LastAccessed.IsZero() {
		return ErrInvalidEntry
	}
	return nil
}

// Stats summarizes tracked and artifact-protected cache usage.
type Stats struct {
	Entries          int64
	Bytes            int64
	ProtectedEntries int64
	ProtectedBytes   int64
}

// Validate checks persisted aggregate values.
func (stats Stats) Validate() error {
	if stats.Entries < 0 || stats.Bytes < 0 || stats.ProtectedEntries < 0 ||
		stats.ProtectedBytes < 0 || stats.ProtectedEntries > stats.Entries ||
		stats.ProtectedBytes > stats.Bytes {
		return ErrInvalidEntry
	}
	return nil
}

// QuotaError reports why protected or actively used chunks prevented pruning.
type QuotaError struct {
	MaximumBytes int64
	CurrentBytes int64
	PinnedBytes  int64
}

func (err *QuotaError) Error() string {
	if err == nil {
		return ErrQuotaExceeded.Error()
	}
	return fmt.Sprintf(
		"%s: %d bytes used with %d bytes pinned (maximum %d)",
		ErrQuotaExceeded,
		err.CurrentBytes,
		err.PinnedBytes,
		err.MaximumBytes,
	)
}

func (err *QuotaError) Unwrap() error { return ErrQuotaExceeded }

// ValidateMaximumBytes checks the daemon's configured hard cache limit.
func ValidateMaximumBytes(value int64) error {
	if value < MinimumMaximumBytes || value > MaximumMaximumBytes {
		return fmt.Errorf(
			"%w: must be between %d and %d bytes",
			ErrInvalidQuota,
			MinimumMaximumBytes,
			MaximumMaximumBytes,
		)
	}
	return nil
}

// Repository durably indexes cache entries and artifact-held references.
type Repository interface {
	Record(context.Context, Entry) error
	Delete(context.Context, snapshot.Digest) error
	Reconcile(context.Context, []Entry) error
	Stats(context.Context) (Stats, error)
	EvictionCandidates(context.Context) ([]Entry, error)
}
