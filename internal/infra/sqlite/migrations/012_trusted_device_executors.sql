ALTER TABLE trusted_devices
    ADD COLUMN supported_executors_json TEXT NOT NULL DEFAULT '[]'
    CHECK (length(supported_executors_json) BETWEEN 2 AND 512);
