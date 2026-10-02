-- +goose Up
DROP TRIGGER accounts_manager_removal_insert;
DROP TRIGGER accounts_manager_removal_update;
ALTER TABLE accounts_manager_removals RENAME TO accounts_manager_removals_legacy;
CREATE TABLE accounts_manager_removals (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL CHECK (length(account_id) > 0),
    impact TEXT NOT NULL CHECK (json_valid(impact)),
    phase TEXT NOT NULL CHECK (phase IN ('requested', 'stopping', 'revoked', 'complete', 'recovery_required', 'cancelled')),
    error_code TEXT NOT NULL DEFAULT '',
    stop_started INTEGER NOT NULL DEFAULT 0 CHECK (stop_started IN (0, 1)),
    bindings_revoked INTEGER NOT NULL DEFAULT 0 CHECK (bindings_revoked IN (0, 1)),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO accounts_manager_removals(id, account_id, impact, phase, error_code, stop_started, created_at, updated_at)
SELECT id, account_id, impact, phase, error_code, 1, created_at, updated_at FROM accounts_manager_removals_legacy;
DROP TABLE accounts_manager_removals_legacy;
CREATE UNIQUE INDEX accounts_manager_removal_account ON accounts_manager_removals(account_id) WHERE phase != 'cancelled';

CREATE TABLE accounts_manager_removed_choices (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    account_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (session_id, provider)
);

CREATE TABLE accounts_manager_chat_hosts (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    generation TEXT NOT NULL CHECK (length(generation) > 0),
    host_identity TEXT NOT NULL CHECK (length(host_identity) = 64),
    PRIMARY KEY (session_id, generation)
);

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removal_insert AFTER INSERT ON accounts_manager_removals
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log(project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id), NEW.updated_at
    FROM sessions AS s JOIN accounts_manager_session_bindings AS b ON b.session_id = s.id
    WHERE b.account_id = NEW.account_id;
    INSERT OR IGNORE INTO accounts_manager_queue_obligations(turn_id)
    SELECT t.id FROM conversation_turns AS t JOIN accounts_manager_session_bindings AS b ON b.session_id = t.handled_by_session_id
    WHERE t.state = 'queued' AND b.account_id = NEW.account_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removal_update AFTER UPDATE ON accounts_manager_removals
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log(project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id), NEW.updated_at
    FROM sessions AS s JOIN json_each(NEW.impact, '$.Sessions') AS entry ON s.id = json_extract(entry.value, '$.SessionID');
    INSERT OR IGNORE INTO accounts_manager_queue_obligations(turn_id)
    SELECT t.id FROM conversation_turns AS t JOIN json_each(NEW.impact, '$.Sessions') AS entry ON t.handled_by_session_id = json_extract(entry.value, '$.SessionID')
    WHERE t.state = 'queued';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removed_choice_insert AFTER INSERT ON accounts_manager_removed_choices
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_removed_choice_delete AFTER DELETE ON accounts_manager_removed_choices
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
END;
-- +goose StatementEnd

-- +goose Down
-- Deleted-choice fences cannot be represented by the prior schema.
SELECT RAISE(ABORT, 'account deletion coordination cannot be downgraded');
