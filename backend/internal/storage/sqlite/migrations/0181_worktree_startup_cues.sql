-- Summary: opt in one command Cue per project and persist its session startup outcome.
-- +goose Up
ALTER TABLE cues ADD COLUMN run_on_worktree_creation INTEGER NOT NULL DEFAULT 0 CHECK (run_on_worktree_creation IN (0, 1) AND (run_on_worktree_creation = 0 OR type = 'command'));
ALTER TABLE cues ADD COLUMN startup_shell TEXT NOT NULL DEFAULT '';
ALTER TABLE cues ADD COLUMN startup_timeout_seconds INTEGER NOT NULL DEFAULT 600 CHECK (startup_timeout_seconds BETWEEN 1 AND 86400);
CREATE UNIQUE INDEX cues_one_worktree_startup ON cues(project_id) WHERE run_on_worktree_creation = 1;
ALTER TABLE sessions ADD COLUMN startup_cue_json TEXT NOT NULL DEFAULT '';
-- +goose StatementBegin
CREATE TRIGGER sessions_startup_cue_cdc
AFTER UPDATE OF startup_cue_json ON sessions
WHEN OLD.startup_cue_json <> NEW.startup_cue_json
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'startupCue', json(NULLIF(NEW.startup_cue_json, ''))),
        NEW.updated_at);
END;
-- +goose StatementEnd
CREATE TABLE startup_cue_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message TEXT NOT NULL,
    client_message_id TEXT NOT NULL UNIQUE,
    delivered INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE startup_cue_messages;
DROP TRIGGER sessions_startup_cue_cdc;
ALTER TABLE sessions DROP COLUMN startup_cue_json;
DROP INDEX cues_one_worktree_startup;
ALTER TABLE cues DROP COLUMN startup_timeout_seconds;
ALTER TABLE cues DROP COLUMN startup_shell;
ALTER TABLE cues DROP COLUMN run_on_worktree_creation;
