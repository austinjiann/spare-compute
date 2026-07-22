ALTER TABLE jobs
    ADD COLUMN outputs_json TEXT NOT NULL DEFAULT '[]';

CREATE TABLE job_artifacts (
    job_id TEXT PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    manifest_id TEXT NOT NULL CHECK (length(manifest_id) = 64),
    manifest_json TEXT NOT NULL CHECK (length(manifest_json) BETWEEN 2 AND 2097152),
    total_bytes INTEGER NOT NULL CHECK (total_bytes >= 0),
    collected_at_ns INTEGER NOT NULL CHECK (collected_at_ns > 0)
) STRICT;

CREATE INDEX job_artifacts_collected_at_idx
    ON job_artifacts (collected_at_ns DESC, job_id);
