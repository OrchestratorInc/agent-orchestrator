-- +goose Up
-- +goose StatementBegin
CREATE TABLE session_artifact_versions (
    session_id   TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    path         TEXT      NOT NULL,
    version      INTEGER   NOT NULL CHECK (version >= 1),
    sha256       TEXT      NOT NULL,
    size         INTEGER   NOT NULL CHECK (size >= 0),
    content_type TEXT      NOT NULL,
    mtime        INTEGER   NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, path, version)
);

CREATE INDEX idx_session_artifact_versions_sha
    ON session_artifact_versions(session_id, sha256);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_session_artifact_versions_sha;
DROP TABLE IF EXISTS session_artifact_versions;
-- +goose StatementEnd
