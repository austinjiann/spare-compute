CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    executable TEXT NOT NULL,
    arguments_json TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    environment_json TEXT NOT NULL,
    executor TEXT NOT NULL CHECK (executor IN ('native', 'container')),
    container_image TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'created',
        'validating',
        'queued',
        'snapshotting',
        'transferring',
        'starting',
        'running',
        'collecting',
        'restoring',
        'succeeded',
        'failed',
        'cancelled',
        'rejected',
        'lost'
    )),
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    failure_code TEXT,
    failure_message TEXT,
    failure_retryable INTEGER,
    CHECK (updated_at_ns >= created_at_ns),
    CHECK (
        (
            state IN ('failed', 'rejected', 'lost')
            AND failure_code IS NOT NULL
            AND failure_message IS NOT NULL
            AND failure_retryable IS NOT NULL
        )
        OR
        (
            state NOT IN ('failed', 'rejected', 'lost')
            AND failure_code IS NULL
            AND failure_message IS NULL
            AND failure_retryable IS NULL
        )
    )
) STRICT;

CREATE INDEX jobs_updated_at_idx ON jobs (updated_at_ns DESC, id ASC);
CREATE INDEX jobs_state_updated_at_idx ON jobs (state, updated_at_ns DESC, id ASC);

CREATE TABLE job_transitions (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    at_ns INTEGER NOT NULL,
    failure_code TEXT,
    failure_message TEXT,
    failure_retryable INTEGER
) STRICT;

CREATE INDEX job_transitions_job_sequence_idx
    ON job_transitions (job_id, sequence);
