-- +goose Up
-- Which AO-managed harness release each session launch started from, so a
-- release directory is deleted only once no live session runs it. The current
-- release per harness is written when an update switches to it; a trigger
-- records it for every new launch (spawn, restore, resume, agent or interface
-- switch), so no launch path can forget. A live Chat reconnect, which keeps the
-- old process under a new generation, carries the old row over explicitly.
CREATE TABLE managed_harness_releases (
    harness    TEXT PRIMARY KEY,
    release    TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE session_harness_releases (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    launch     TEXT NOT NULL,
    harness    TEXT NOT NULL,
    release    TEXT NOT NULL,
    PRIMARY KEY (session_id, launch)
);

-- +goose StatementBegin
CREATE TRIGGER sessions_record_harness_release_update
AFTER UPDATE OF controller_generation, runtime_launch_id, harness ON sessions
WHEN OLD.controller_generation IS NOT NEW.controller_generation
    OR OLD.runtime_launch_id IS NOT NEW.runtime_launch_id
    OR OLD.harness IS NOT NEW.harness
BEGIN
    INSERT OR IGNORE INTO session_harness_releases (session_id, launch, harness, release)
    SELECT NEW.id,
        CASE WHEN NEW.session_mode = 'chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END,
        NEW.harness, m.release
    FROM managed_harness_releases m
    WHERE m.harness = NEW.harness
        AND (CASE WHEN NEW.session_mode = 'chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END) <> '';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_record_harness_release_insert
AFTER INSERT ON sessions
BEGIN
    INSERT OR IGNORE INTO session_harness_releases (session_id, launch, harness, release)
    SELECT NEW.id,
        CASE WHEN NEW.session_mode = 'chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END,
        NEW.harness, m.release
    FROM managed_harness_releases m
    WHERE m.harness = NEW.harness
        AND (CASE WHEN NEW.session_mode = 'chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END) <> '';
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER sessions_record_harness_release_insert;
DROP TRIGGER sessions_record_harness_release_update;
DROP TABLE session_harness_releases;
DROP TABLE managed_harness_releases;
