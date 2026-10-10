-- +goose Up
-- Some development databases recorded migration 0125 as applied before its
-- failure-observability tables were created. Recreate the missing objects
-- without touching any existing rows so those databases can start safely.
CREATE TABLE IF NOT EXISTS agent_switch_failure_policy (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    consent_generation TEXT NOT NULL,
    destination_fingerprint TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

INSERT OR IGNORE INTO agent_switch_failure_policy (
    singleton, enabled, consent_generation, destination_fingerprint, updated_at
) VALUES (1, 0, '', '', CURRENT_TIMESTAMP);

CREATE TABLE IF NOT EXISTS agent_switch_failure_receipts (
    dedupe_key TEXT PRIMARY KEY,
    switch_id TEXT REFERENCES agent_switches(id) ON DELETE CASCADE,
    report_kind TEXT NOT NULL,
    durable_state_fingerprint TEXT NOT NULL,
    recorded_at TIMESTAMP NOT NULL,
    retain_until TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_switch_failure_outbox (
    id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    envelope_encoding_version INTEGER NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    destination_fingerprint TEXT NOT NULL,
    switch_id TEXT,
    report_kind TEXT NOT NULL,
    scope TEXT NOT NULL,
    failure_point TEXT NOT NULL,
    classifier_callsite TEXT NOT NULL,
    phase TEXT NOT NULL,
    error_code TEXT NOT NULL,
    fault_code TEXT NOT NULL,
    execution TEXT NOT NULL,
    execution_attempt_id TEXT NOT NULL,
    mode TEXT NOT NULL,
    from_harness TEXT NOT NULL,
    target_harness TEXT NOT NULL,
    target_start_mode TEXT NOT NULL,
    runtime_backend TEXT NOT NULL,
    call_outcome TEXT NOT NULL,
    ownership TEXT NOT NULL,
    compensation TEXT NOT NULL,
    user_impact TEXT NOT NULL,
    source_stop_confirmed TEXT NOT NULL,
    target_owner_committed TEXT NOT NULL,
    gate_retained TEXT NOT NULL,
    requested_at TIMESTAMP,
    occurred_at TIMESTAMP NOT NULL,
    sanitized_stack BLOB NOT NULL,
    stack_fingerprint TEXT NOT NULL,
    canonical_event_json BLOB NOT NULL CHECK (length(canonical_event_json) <= 61440),
    expires_at TIMESTAMP NOT NULL,
    available_at TIMESTAMP NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMP,
    lease_token TEXT,
    lease_consent_generation TEXT,
    lease_delivery_epoch INTEGER,
    lease_expires_at TIMESTAMP,
    delivered_at TIMESTAMP,
    discarded_at TIMESTAMP,
    last_delivery_error_class TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_switch_failure_outbox_pending
    ON agent_switch_failure_outbox(available_at, occurred_at)
    WHERE delivered_at IS NULL AND discarded_at IS NULL;

CREATE TABLE IF NOT EXISTS agent_switch_failure_delivery_state (
    destination_fingerprint TEXT PRIMARY KEY,
    error_not_before TIMESTAMP,
    all_not_before TIMESTAMP
);

-- +goose Down
SELECT 1;
