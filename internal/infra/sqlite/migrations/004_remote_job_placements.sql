CREATE TABLE remote_job_placements (
    job_id TEXT PRIMARY KEY,
    worker_device_id TEXT NOT NULL,
    placed_at_ns INTEGER NOT NULL,
    FOREIGN KEY (worker_device_id) REFERENCES trusted_devices(device_id),
    CHECK (length(job_id) = 36),
    CHECK (length(worker_device_id) = 52),
    CHECK (placed_at_ns > 0)
) STRICT;

CREATE INDEX remote_job_placements_worker_time_idx
    ON remote_job_placements (worker_device_id, placed_at_ns DESC);
