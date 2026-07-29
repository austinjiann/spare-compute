CREATE TABLE content_cache_entries (
    digest TEXT PRIMARY KEY CHECK (length(digest) = 64),
    size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 524288),
    last_accessed_ns INTEGER NOT NULL CHECK (last_accessed_ns > 0)
) STRICT;

CREATE INDEX content_cache_entries_lru_idx
    ON content_cache_entries (last_accessed_ns ASC, digest ASC);

ALTER TABLE job_artifacts
    ADD COLUMN retrieved_at_ns INTEGER
        CHECK (retrieved_at_ns IS NULL OR retrieved_at_ns >= collected_at_ns);

CREATE TABLE content_cache_artifact_refs (
    job_id TEXT NOT NULL REFERENCES job_artifacts (job_id) ON DELETE CASCADE,
    digest TEXT NOT NULL CHECK (length(digest) = 64),
    PRIMARY KEY (job_id, digest)
) STRICT;

CREATE INDEX content_cache_artifact_refs_digest_idx
    ON content_cache_artifact_refs (digest);
