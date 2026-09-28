-- +goose Up
CREATE TABLE accounts_manager_removals (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL UNIQUE CHECK (length(account_id) > 0),
    impact TEXT NOT NULL CHECK (json_valid(impact)),
    phase TEXT NOT NULL CHECK (phase IN ('stopping', 'revoked', 'complete', 'recovery_required')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removal_insert AFTER INSERT ON accounts_manager_removals
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id), NEW.updated_at
    FROM sessions AS s JOIN accounts_manager_session_bindings AS b ON b.session_id = s.id
    WHERE b.connection_mode = 'managed' AND b.account_id = NEW.account_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removal_update AFTER UPDATE ON accounts_manager_removals
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id), NEW.updated_at
    FROM sessions AS s JOIN accounts_manager_session_bindings AS b ON b.session_id = s.id
    WHERE b.connection_mode = 'managed' AND b.account_id = NEW.account_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE accounts_manager_removals;
