-- +goose Up
-- +goose StatementBegin
-- One row per deliverable: a file or bundle directory in the session's artifact
-- directory that ao artifact add registered. The files themselves stay on disk,
-- so a row can outlive its file; it is ignored until the path is written again.
CREATE TABLE session_artifacts (
    session_id    TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    path          TEXT      NOT NULL,
    registered_at TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, path)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS session_artifacts;
-- +goose StatementEnd
