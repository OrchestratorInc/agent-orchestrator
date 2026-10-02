-- +goose Up
CREATE TABLE accounts_manager_switches (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude')),
    source_mode TEXT NOT NULL CHECK (source_mode IN ('native', 'managed')),
    source_account_id TEXT NOT NULL,
    source_revision INTEGER NOT NULL CHECK (source_revision > 0),
    source_owner TEXT NOT NULL CHECK (json_valid(source_owner)),
    source_runtime_handle_id TEXT NOT NULL,
    target_mode TEXT NOT NULL CHECK (target_mode IN ('native', 'managed')),
    target_account_id TEXT NOT NULL,
    target_revision INTEGER NOT NULL DEFAULT 0 CHECK (target_revision >= 0),
    target_generation TEXT NOT NULL CHECK (length(target_generation) > 0),
    policy TEXT NOT NULL CHECK (policy IN ('drain', 'interrupt')),
    new_conversation INTEGER NOT NULL DEFAULT 0 CHECK (new_conversation IN (0, 1)),
    phase TEXT NOT NULL CHECK (phase IN ('requested', 'waiting', 'stopping', 'stopped', 'committed', 'starting', 'ready', 'cancelled', 'failed', 'recovery_required')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CHECK ((source_mode = 'native' AND source_account_id = '') OR (source_mode = 'managed' AND length(source_account_id) > 0)),
    CHECK ((target_mode = 'native' AND target_account_id = '') OR (target_mode = 'managed' AND length(target_account_id) > 0))
);
CREATE UNIQUE INDEX accounts_manager_switches_active_session ON accounts_manager_switches(session_id)
WHERE phase NOT IN ('ready', 'cancelled', 'failed');
CREATE INDEX accounts_manager_switches_session_history ON accounts_manager_switches(session_id, created_at DESC);

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_switches_insert AFTER INSERT ON accounts_manager_switches
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT project_id, id, 'session_updated', json_object('id', id), NEW.updated_at FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_switches_update AFTER UPDATE ON accounts_manager_switches
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT project_id, id, 'session_updated', json_object('id', id), NEW.updated_at FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_switches_delete AFTER DELETE ON accounts_manager_switches
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE accounts_manager_switches;
