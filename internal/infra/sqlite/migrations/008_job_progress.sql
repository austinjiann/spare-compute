CREATE TABLE job_progress (
    job_id TEXT PRIMARY KEY CHECK (length(job_id) = 36),
    phase TEXT NOT NULL CHECK (phase IN ('snapshot', 'upload', 'download', 'restore', 'collect')),
    completed_bytes INTEGER NOT NULL CHECK (completed_bytes >= 0),
    total_bytes INTEGER NOT NULL CHECK (total_bytes > 0),
    updated_at_ns INTEGER NOT NULL CHECK (updated_at_ns > 0),
    CHECK (completed_bytes <= total_bytes)
) STRICT;

CREATE INDEX job_progress_updated_at_idx
    ON job_progress (updated_at_ns DESC, job_id ASC);
