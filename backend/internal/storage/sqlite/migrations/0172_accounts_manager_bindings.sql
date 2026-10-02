-- +goose Up
CREATE TABLE accounts_manager_session_bindings (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude')),
    connection_mode TEXT NOT NULL CHECK (connection_mode IN ('native', 'managed')),
    account_id TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision > 0),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (session_id, provider),
    CHECK ((connection_mode = 'native' AND account_id = '') OR (connection_mode = 'managed' AND length(account_id) > 0))
);

INSERT INTO accounts_manager_session_bindings
    (session_id, provider, connection_mode, account_id, revision, created_at, updated_at)
SELECT session_id, provider, 'managed', account_id, 1, created_at, updated_at
FROM accounts_manager_session_routes;

INSERT OR IGNORE INTO accounts_manager_session_bindings
    (session_id, provider, connection_mode, account_id, revision, created_at, updated_at)
SELECT id, CASE harness WHEN 'codex' THEN 'codex' ELSE 'claude' END,
       'native', '', 1, created_at, updated_at
FROM sessions WHERE harness IN ('codex', 'claude-code');

CREATE TABLE accounts_manager_binding_clock (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision > 0)
);
INSERT INTO accounts_manager_binding_clock (id, revision) VALUES (1, 1);

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_bindings_insert AFTER INSERT ON accounts_manager_session_bindings
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT project_id, id, 'session_updated', json_object('id', id), NEW.updated_at
    FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_bindings_update AFTER UPDATE ON accounts_manager_session_bindings
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT project_id, id, 'session_updated', json_object('id', id), NEW.updated_at
    FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_bindings_delete AFTER DELETE ON accounts_manager_session_bindings
BEGIN
    UPDATE accounts_manager_binding_clock SET revision = revision + 1 WHERE id = 1;
END;
-- +goose StatementEnd

-- +goose Down
DELETE FROM accounts_manager_session_routes;
INSERT INTO accounts_manager_session_routes (session_id, provider, account_id, created_at, updated_at)
SELECT session_id, provider, account_id, created_at, updated_at
FROM accounts_manager_session_bindings WHERE connection_mode = 'managed';
DROP TABLE accounts_manager_session_bindings;
DROP TABLE accounts_manager_binding_clock;
