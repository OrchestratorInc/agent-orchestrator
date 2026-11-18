-- +goose Up
-- +goose StatementBegin
-- The artifacts of a session deleted with its artifacts kept. The files stay
-- in the session's artifact directory, <dataDir>/artifacts/<session_id>: a
-- session number is never reused, so no later session writes there. A row here
-- is only listed once its session row is gone, so a delete that fails after
-- the artifacts were recorded leaves nothing extra in view.
--
-- project_id is the project the session was in, NULL for one in no project.
-- title is the session's title when it was deleted.
CREATE TABLE kept_artifact_sessions (
    session_id TEXT      NOT NULL PRIMARY KEY,
    project_id TEXT,
    title      TEXT      NOT NULL DEFAULT '',
    kept_at    TIMESTAMP NOT NULL
);

-- The session_artifacts rows of a kept session, so a kept list still tells
-- registered deliverables from the other files and keeps each artifact's id.
CREATE TABLE kept_artifacts (
    id             TEXT      NOT NULL PRIMARY KEY,
    session_id     TEXT      NOT NULL REFERENCES kept_artifact_sessions(session_id) ON DELETE CASCADE,
    path           TEXT      NOT NULL,
    registered_at  TIMESTAMP,
    previous_paths TEXT      NOT NULL DEFAULT '[]',
    created_at     TIMESTAMP NOT NULL,
    UNIQUE (session_id, path)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS kept_artifacts;
DROP TABLE IF EXISTS kept_artifact_sessions;
-- +goose StatementEnd
