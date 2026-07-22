package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

// ArtifactRepository stores immutable collected-output manifests.
type ArtifactRepository struct {
	database *sql.DB
}

var _ artifact.Repository = (*ArtifactRepository)(nil)

// Save inserts one bundle or accepts an idempotent retry of identical content.
func (repository *ArtifactRepository) Save(ctx context.Context, bundle artifact.Bundle) error {
	if err := bundle.Validate(); err != nil {
		return err
	}
	manifestID, err := bundle.Manifest.ID()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(bundle.Manifest)
	if err != nil {
		return fmt.Errorf("encode artifact manifest: %w", err)
	}
	if len(encoded) > artifact.MaximumStoredManifestBytes {
		return artifact.ErrInvalidBundle
	}
	_, err = repository.database.ExecContext(ctx, `
		INSERT INTO job_artifacts (
			job_id, manifest_id, manifest_json, total_bytes, collected_at_ns
		) VALUES (?, ?, ?, ?, ?)
	`, bundle.JobID, manifestID, string(encoded), bundle.Manifest.TotalBytes, bundle.CollectedAt.UTC().UnixNano())
	if err == nil {
		return nil
	}
	if !isConstraintError(err) {
		return fmt.Errorf("save job artifacts: %w", err)
	}
	existing, getErr := repository.Get(ctx, bundle.JobID)
	if getErr != nil {
		return errors.Join(artifact.ErrConflict, getErr)
	}
	existingID, _ := existing.Manifest.ID()
	// Collection is content-addressed. A runner retry after a crash may observe
	// the same immutable output with a later clock value; retain the first
	// durable timestamp and accept the identical manifest idempotently.
	if existingID == manifestID {
		return nil
	}
	return artifact.ErrConflict
}

// Get loads and revalidates one collected artifact bundle.
func (repository *ArtifactRepository) Get(ctx context.Context, id job.ID) (artifact.Bundle, error) {
	if !id.Valid() {
		return artifact.Bundle{}, job.ErrInvalidID
	}
	var manifestID string
	var encoded string
	var totalBytes int64
	var collectedAtNS int64
	err := repository.database.QueryRowContext(ctx, `
		SELECT manifest_id, manifest_json, total_bytes, collected_at_ns
		FROM job_artifacts
		WHERE job_id = ?
	`, id).Scan(&manifestID, &encoded, &totalBytes, &collectedAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.Bundle{}, fmt.Errorf("%w: %s", artifact.ErrNotFound, id)
	}
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf("get job artifacts: %w", err)
	}
	if len(encoded) > artifact.MaximumStoredManifestBytes {
		return artifact.Bundle{}, artifact.ErrInvalidBundle
	}
	claimedID, err := snapshot.ParseDigest(manifestID)
	if err != nil {
		return artifact.Bundle{}, artifact.ErrInvalidBundle
	}
	var manifest snapshot.Manifest
	if err := json.Unmarshal([]byte(encoded), &manifest); err != nil {
		return artifact.Bundle{}, fmt.Errorf("decode artifact manifest: %w", err)
	}
	actualID, err := manifest.ID()
	if err != nil || actualID != claimedID || manifest.TotalBytes != totalBytes {
		return artifact.Bundle{}, artifact.ErrInvalidBundle
	}
	bundle := artifact.Bundle{
		JobID: id, Manifest: manifest, CollectedAt: time.Unix(0, collectedAtNS).UTC(),
	}
	if err := bundle.Validate(); err != nil {
		return artifact.Bundle{}, err
	}
	return bundle, nil
}
