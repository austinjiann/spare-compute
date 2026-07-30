ALTER TABLE trusted_devices
    ADD COLUMN tool_ids_json TEXT NOT NULL DEFAULT '[]'
    CHECK (length(tool_ids_json) BETWEEN 2 AND 8192);
