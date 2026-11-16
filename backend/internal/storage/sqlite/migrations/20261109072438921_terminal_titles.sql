-- +goose Up
-- A name the user chose outranks a title the terminal program emits; a name AO
-- generated or derived from the prompt does not. The two could not be told apart
-- before this column, so every existing name is kept as intentional.
ALTER TABLE sessions ADD COLUMN display_name_explicit BOOLEAN NOT NULL DEFAULT 0;
UPDATE sessions SET display_name_explicit = 1 WHERE display_name <> '';

-- Shell tabs get a numbered default title at creation, so an existing title is
-- intentional only when it does not have that shape.
ALTER TABLE shell_terminals ADD COLUMN title_explicit BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE shell_terminals ADD COLUMN app_title TEXT NOT NULL DEFAULT '';
UPDATE shell_terminals SET title_explicit = 1
WHERE NOT (title = 'Terminal' OR title GLOB 'Terminal [0-9]*');

-- Separate from sessions because every sessions update bumps the revision that
-- fences observer writes, and a program's title is not a lifecycle fact.
CREATE TABLE session_terminal_titles (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER session_terminal_titles_cdc_insert
AFTER INSERT ON session_terminal_titles
WHEN NEW.title <> ''
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'terminalTitle', NEW.title), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_terminal_titles_cdc_update
AFTER UPDATE ON session_terminal_titles
WHEN OLD.title <> NEW.title
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'terminalTitle', NEW.title), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = NEW.session_id;
END;
-- +goose StatementEnd

-- Shells are off the board and refetch only on this event. Standalone shells
-- are shown nowhere, and change_log requires a project, so only scoped ones emit.
-- +goose StatementBegin
CREATE TRIGGER shell_terminals_app_title_cdc
AFTER UPDATE OF app_title ON shell_terminals
WHEN OLD.app_title <> NEW.app_title AND NEW.session_id IS NOT NULL
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'shellTerminalHandleId', NEW.handle_id), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS shell_terminals_app_title_cdc;
DROP TRIGGER IF EXISTS session_terminal_titles_cdc_update;
DROP TRIGGER IF EXISTS session_terminal_titles_cdc_insert;
DROP TABLE IF EXISTS session_terminal_titles;
ALTER TABLE shell_terminals DROP COLUMN app_title;
ALTER TABLE shell_terminals DROP COLUMN title_explicit;
ALTER TABLE sessions DROP COLUMN display_name_explicit;
