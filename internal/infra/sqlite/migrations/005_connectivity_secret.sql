ALTER TABLE trusted_devices
    ADD COLUMN connectivity_secret BLOB
    CHECK (connectivity_secret IS NULL OR length(connectivity_secret) = 32);
