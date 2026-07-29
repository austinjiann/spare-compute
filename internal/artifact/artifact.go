// Package artifact defines durable, hash-verified job outputs.
package artifact

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

const MaximumStoredManifestBytes = 2 << 20

var (
	ErrInvalidBundle      = errors.New("invalid artifact bundle")
	ErrNotFound           = errors.New("job artifacts not found")
	ErrConflict           = errors.New("job artifacts already differ")
	ErrInvalidDestination = errors.New("invalid artifact destination")
)

// Bundle is one immutable set of declared outputs collected by a worker.
type Bundle struct {
	JobID       job.ID
	Manifest    snapshot.Manifest
	CollectedAt time.Time
}

// Validate checks durable artifact metadata and its canonical manifest.
func (bundle Bundle) Validate() error {
	if !bundle.JobID.Valid() || bundle.CollectedAt.IsZero() {
		return ErrInvalidBundle
	}
	if _, err := bundle.Manifest.CanonicalBytes(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	return nil
}

// Clone returns a bundle with independent manifest slices.
func (bundle Bundle) Clone() Bundle {
	clone := bundle
	clone.Manifest = bundle.Manifest.Clone()
	return clone
}

// Repository stores one immutable artifact bundle per job.
type Repository interface {
	Save(context.Context, Bundle) error
	Get(context.Context, job.ID) (Bundle, error)
	MarkRetrieved(context.Context, job.ID, time.Time) error
}

// RestoreResult describes files placed without overwriting local content.
type RestoreResult struct {
	Destination string
	Restored    []string
	Conflicts   []string
}

// Validate checks a result returned across application boundaries.
func (result RestoreResult) Validate() error {
	if !filepath.IsAbs(result.Destination) {
		return ErrInvalidDestination
	}
	for _, values := range [][]string{result.Restored, result.Conflicts} {
		previous := ""
		for _, value := range values {
			if snapshot.ValidatePath(value) != nil || previous != "" && value <= previous {
				return ErrInvalidBundle
			}
			previous = value
		}
	}
	return nil
}

// NormalizeResult sorts independent copies for stable CLI and protocol output.
func NormalizeResult(result RestoreResult) RestoreResult {
	result.Restored = append([]string(nil), result.Restored...)
	result.Conflicts = append([]string(nil), result.Conflicts...)
	sort.Strings(result.Restored)
	sort.Strings(result.Conflicts)
	return result
}
