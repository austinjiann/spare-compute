package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/snapshot"
)

// ContentCacheRepository persists verified chunk sizes and LRU access times.
type ContentCacheRepository struct {
	database *sql.DB
}

var _ contentcache.Repository = (*ContentCacheRepository)(nil)

// Record inserts or touches one verified cache entry.
func (repository *ContentCacheRepository) Record(ctx context.Context, entry contentcache.Entry) error {
	if repository == nil || repository.database == nil {
		return contentcache.ErrInvalidRepository
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO content_cache_entries (digest, size_bytes, last_accessed_ns)
		VALUES (?, ?, ?)
		ON CONFLICT (digest) DO UPDATE SET
			size_bytes = excluded.size_bytes,
			last_accessed_ns = excluded.last_accessed_ns
	`, entry.Digest, entry.Size, entry.LastAccessed.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("record content cache entry: %w", err)
	}
	return nil
}

// Delete forgets one absent or evicted chunk.
func (repository *ContentCacheRepository) Delete(ctx context.Context, digest snapshot.Digest) error {
	if repository == nil || repository.database == nil {
		return contentcache.ErrInvalidRepository
	}
	if !digest.Valid() {
		return snapshot.ErrInvalidDigest
	}
	if _, err := repository.database.ExecContext(
		ctx,
		"DELETE FROM content_cache_entries WHERE digest = ?",
		digest,
	); err != nil {
		return fmt.Errorf("delete content cache entry: %w", err)
	}
	return nil
}

// Reconcile adopts legacy disk entries without discarding newer persisted
// access times and removes metadata for chunks no longer on disk.
func (repository *ContentCacheRepository) Reconcile(
	ctx context.Context,
	observed []contentcache.Entry,
) error {
	if repository == nil || repository.database == nil {
		return contentcache.ErrInvalidRepository
	}
	byDigest := make(map[snapshot.Digest]contentcache.Entry, len(observed))
	for _, entry := range observed {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := byDigest[entry.Digest]; exists {
			return contentcache.ErrInvalidEntry
		}
		byDigest[entry.Digest] = entry
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin content cache reconciliation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	rows, err := transaction.QueryContext(ctx, "SELECT digest FROM content_cache_entries")
	if err != nil {
		return fmt.Errorf("list indexed content cache entries: %w", err)
	}
	indexed := make([]snapshot.Digest, 0)
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan indexed content cache entry: %w", err)
		}
		digest, err := snapshot.ParseDigest(encoded)
		if err != nil {
			_ = rows.Close()
			return contentcache.ErrInvalidEntry
		}
		indexed = append(indexed, digest)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("iterate indexed content cache entries: %w", err)
	}
	for _, digest := range indexed {
		if _, present := byDigest[digest]; present {
			continue
		}
		if _, err := transaction.ExecContext(
			ctx,
			"DELETE FROM content_cache_entries WHERE digest = ?",
			digest,
		); err != nil {
			return fmt.Errorf("remove stale content cache entry: %w", err)
		}
	}
	for _, entry := range observed {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO content_cache_entries (digest, size_bytes, last_accessed_ns)
			VALUES (?, ?, ?)
			ON CONFLICT (digest) DO UPDATE SET size_bytes = excluded.size_bytes
		`, entry.Digest, entry.Size, entry.LastAccessed.UTC().UnixNano()); err != nil {
			return fmt.Errorf("adopt content cache entry: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit content cache reconciliation: %w", err)
	}
	return nil
}

// Stats returns total and artifact-protected cache usage.
func (repository *ContentCacheRepository) Stats(ctx context.Context) (contentcache.Stats, error) {
	if repository == nil || repository.database == nil {
		return contentcache.Stats{}, contentcache.ErrInvalidRepository
	}
	var result contentcache.Stats
	err := repository.database.QueryRowContext(ctx, `
		WITH active_job AS (
			SELECT EXISTS(
				SELECT 1 FROM jobs WHERE state IN ('starting', 'running', 'collecting')
			) AS present
		)
		SELECT
			COUNT(*),
			COALESCE(SUM(entry.size_bytes), 0),
			COALESCE(SUM(CASE WHEN (SELECT present FROM active_job) OR EXISTS (
				SELECT 1
				FROM content_cache_artifact_refs AS reference
				JOIN job_artifacts AS artifacts ON artifacts.job_id = reference.job_id
				WHERE reference.digest = entry.digest AND artifacts.retrieved_at_ns IS NULL
			) THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN (SELECT present FROM active_job) OR EXISTS (
				SELECT 1
				FROM content_cache_artifact_refs AS reference
				JOIN job_artifacts AS artifacts ON artifacts.job_id = reference.job_id
				WHERE reference.digest = entry.digest AND artifacts.retrieved_at_ns IS NULL
			) THEN entry.size_bytes ELSE 0 END), 0)
		FROM content_cache_entries AS entry
	`).Scan(&result.Entries, &result.Bytes, &result.ProtectedEntries, &result.ProtectedBytes)
	if err != nil {
		return contentcache.Stats{}, fmt.Errorf("read content cache stats: %w", err)
	}
	if err := result.Validate(); err != nil {
		return contentcache.Stats{}, err
	}
	return result, nil
}

// EvictionCandidates returns unprotected entries from least to most recently used.
func (repository *ContentCacheRepository) EvictionCandidates(
	ctx context.Context,
) ([]contentcache.Entry, error) {
	if repository == nil || repository.database == nil {
		return nil, contentcache.ErrInvalidRepository
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT entry.digest, entry.size_bytes, entry.last_accessed_ns
		FROM content_cache_entries AS entry
		WHERE NOT EXISTS (
			SELECT 1
			FROM content_cache_artifact_refs AS reference
			JOIN job_artifacts AS artifacts ON artifacts.job_id = reference.job_id
			WHERE reference.digest = entry.digest AND artifacts.retrieved_at_ns IS NULL
		)
		AND NOT EXISTS (
			SELECT 1 FROM jobs WHERE state IN ('starting', 'running', 'collecting')
		)
		ORDER BY entry.last_accessed_ns ASC, entry.digest ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list content cache eviction candidates: %w", err)
	}
	defer rows.Close()
	result := make([]contentcache.Entry, 0)
	for rows.Next() {
		var encoded string
		var size int64
		var accessedNS int64
		if err := rows.Scan(&encoded, &size, &accessedNS); err != nil {
			return nil, fmt.Errorf("scan content cache eviction candidate: %w", err)
		}
		digest, err := snapshot.ParseDigest(encoded)
		if err != nil {
			return nil, contentcache.ErrInvalidEntry
		}
		entry := contentcache.Entry{
			Digest: digest, Size: size, LastAccessed: time.Unix(0, accessedNS).UTC(),
		}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate content cache eviction candidates: %w", err)
	}
	if !sort.SliceIsSorted(result, func(left, right int) bool {
		if result[left].LastAccessed.Equal(result[right].LastAccessed) {
			return result[left].Digest < result[right].Digest
		}
		return result[left].LastAccessed.Before(result[right].LastAccessed)
	}) {
		return nil, contentcache.ErrInvalidEntry
	}
	return result, nil
}
