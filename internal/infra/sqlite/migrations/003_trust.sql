CREATE TABLE trusted_devices (
    device_id TEXT PRIMARY KEY,
    pair_id TEXT NOT NULL UNIQUE,
    public_key BLOB NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('worker', 'orchestrator')),
    state TEXT NOT NULL CHECK (state IN ('active', 'revoked')),
    paired_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    revoked_at_ns INTEGER,
    CHECK (length(device_id) = 52),
    CHECK (length(pair_id) = 26),
    CHECK (length(public_key) = 32),
    CHECK (length(name) BETWEEN 1 AND 80),
    CHECK (updated_at_ns >= paired_at_ns),
    CHECK (
        (state = 'active' AND revoked_at_ns IS NULL)
        OR
        (state = 'revoked' AND revoked_at_ns = updated_at_ns AND revoked_at_ns >= paired_at_ns)
    )
) STRICT;

-- A worker can trust only one designated orchestrator. An orchestrator may
-- trust any number of workers because worker rows do not participate here.
CREATE UNIQUE INDEX trusted_devices_one_active_orchestrator_idx
    ON trusted_devices ((CASE WHEN role = 'orchestrator' THEN 1 END))
    WHERE state = 'active' AND role = 'orchestrator';

CREATE INDEX trusted_devices_state_name_idx
    ON trusted_devices (state, name, device_id);

CREATE TABLE trust_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL,
    pair_id TEXT NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('paired', 'revoked')),
    at_ns INTEGER NOT NULL
) STRICT;

CREATE INDEX trust_events_device_sequence_idx
    ON trust_events (device_id, sequence);
