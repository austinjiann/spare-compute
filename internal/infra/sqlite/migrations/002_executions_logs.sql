CREATE TABLE job_executions (
    job_id TEXT PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('starting', 'running', 'completed')),
    runner_pid INTEGER NOT NULL CHECK (runner_pid > 0),
    process_pid INTEGER CHECK (process_pid > 0),
    claimed_at_ns INTEGER NOT NULL,
    started_at_ns INTEGER,
    heartbeat_at_ns INTEGER NOT NULL,
    cancel_requested_at_ns INTEGER,
    finished_at_ns INTEGER,
    exit_code INTEGER,
    termination_signal TEXT NOT NULL DEFAULT '',
    completion TEXT NOT NULL DEFAULT '' CHECK (completion IN ('', 'succeeded', 'failed', 'cancelled')),
    log_bytes INTEGER NOT NULL DEFAULT 0 CHECK (log_bytes >= 0),
    next_log_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_log_sequence > 0),
    CHECK (heartbeat_at_ns >= claimed_at_ns),
    CHECK (cancel_requested_at_ns IS NULL OR cancel_requested_at_ns >= claimed_at_ns),
    CHECK (
        (status = 'starting' AND process_pid IS NULL AND started_at_ns IS NULL AND finished_at_ns IS NULL AND completion = '')
        OR
        (status = 'running' AND process_pid IS NOT NULL AND started_at_ns IS NOT NULL AND finished_at_ns IS NULL AND completion = '')
        OR
        (status = 'completed' AND finished_at_ns IS NOT NULL AND completion <> '')
    )
) STRICT;

CREATE INDEX job_executions_active_heartbeat_idx
    ON job_executions (status, heartbeat_at_ns)
    WHERE status <> 'completed';

CREATE TABLE job_log_records (
    job_id TEXT NOT NULL REFERENCES job_executions (job_id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    stream TEXT NOT NULL CHECK (stream IN ('stdout', 'stderr')),
    data_offset INTEGER NOT NULL CHECK (data_offset >= 0),
    data_length INTEGER NOT NULL CHECK (data_length > 0 AND data_length <= 16384),
    at_ns INTEGER NOT NULL,
    PRIMARY KEY (job_id, sequence),
    UNIQUE (job_id, data_offset)
) STRICT;

CREATE INDEX job_log_records_resume_idx
    ON job_log_records (job_id, sequence);
