ALTER TABLE trusted_devices
    ADD COLUMN platform TEXT NOT NULL DEFAULT ''
    CHECK (length(platform) <= 32 AND instr(platform, '=') = 0);

ALTER TABLE trusted_devices
    ADD COLUMN arch TEXT NOT NULL DEFAULT ''
    CHECK (length(arch) <= 32 AND instr(arch, '=') = 0);

ALTER TABLE trusted_devices
    ADD COLUMN logical_cpu_count INTEGER NOT NULL DEFAULT 0
    CHECK (logical_cpu_count BETWEEN 0 AND 4096);

ALTER TABLE trusted_devices
    ADD COLUMN total_memory_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (total_memory_bytes >= 0);

ALTER TABLE trusted_devices
    ADD COLUMN hints_observed_at_ns INTEGER
    CHECK (hints_observed_at_ns IS NULL OR hints_observed_at_ns > 0);
