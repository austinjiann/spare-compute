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
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin job artifact save: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO job_artifacts (
			job_id, manifest_id, manifest_json, total_bytes, collected_at_ns
		) VALUES (?, ?, ?, ?, ?)
	`, bundle.JobID, manifestID, string(encoded), bundle.Manifest.TotalBytes, bundle.CollectedAt.UTC().UnixNano())
	if err != nil && !isConstraintError(err) {
		return fmt.Errorf("save job artifacts: %w", err)
	}
	if err != nil {
		_ = transaction.Rollback()
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
	for _, digest := range bundle.Manifest.Digests() {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO content_cache_artifact_refs (job_id, digest) VALUES (?, ?)
		`, bundle.JobID, digest); err != nil {
			return fmt.Errorf("protect job artifact content: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit job artifact save: %w", err)
	}
	return nil
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

// MarkRetrieved idempotently makes one successfully restored result eligible
// for normal cache eviction. Missing rows are accepted so an orchestrator can
// use this repository with remote bundles it does not own locally.
func (repository *ArtifactRepository) MarkRetrieved(
	ctx context.Context,
	id job.ID,
	at time.Time,
) error {
	if repository == nil || repository.database == nil || !id.Valid() || at.IsZero() {
		return artifact.ErrInvalidBundle
	}
	result, err := repository.database.ExecContext(ctx, `
		UPDATE job_artifacts
		SET retrieved_at_ns = CASE
			WHEN retrieved_at_ns IS NULL THEN ?
			ELSE retrieved_at_ns
		END
		WHERE job_id = ? AND collected_at_ns <= ?
	`, at.UTC().UnixNano(), id, at.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("mark job artifacts retrieved: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("count retrieved job artifacts: %w", err)
	}
	return nil
}
