-- +goose Up
-- +goose StatementBegin
-- One row per file or bundle directory AO tracks in a session's artifact
-- directory. id is the artifact's identity and never changes: a rename moves
-- the row to the new path, so the artifact keeps its registration, version
-- history and share link. registered_at is when ao artifact add registered it
-- as a deliverable, NULL for a file it did not register. A registered row, or
-- one with version history, outlives its file, and a file written again at the
-- path is the same artifact; any other row goes with its file.
--
-- file_dev, file_ino and file_birth (nanoseconds, 0 where the platform does
-- not report it) name the file on disk that the row last saw. A rename keeps
-- all three, a copy or a newly written file does not, which is how a rename
-- is told apart from a delete and a new file. Linux birth times are coarse, so
-- there file_generation, the inode's generation number, tells a new file from
-- a deleted one whose inode it reused; file_mtime does where the file system
-- has no generation. Both are 0 where they are not needed. previous_paths lists, as a JSON
-- array, the paths the artifact had before, so a link to one still finds it.
CREATE TABLE session_artifacts (
    id             TEXT      NOT NULL PRIMARY KEY,
    session_id     TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    path           TEXT      NOT NULL,
    registered_at  TIMESTAMP,
    file_dev       INTEGER   NOT NULL DEFAULT 0,
    file_ino       INTEGER   NOT NULL DEFAULT 0,
    file_birth     INTEGER   NOT NULL DEFAULT 0,
    file_generation INTEGER  NOT NULL DEFAULT 0,
    file_mtime     INTEGER   NOT NULL DEFAULT 0,
    previous_paths TEXT      NOT NULL DEFAULT '[]',
    created_at     TIMESTAMP NOT NULL,
    UNIQUE (session_id, path)
);

-- Every path with version history becomes an artifact, so the file there, or
-- the next one written there, keeps that history. Its file is not known yet:
-- the first scan that finds a file at the path records it.
INSERT INTO session_artifacts (id, session_id, path, created_at)
SELECT lower(substr(h, 1, 8) || '-' || substr(h, 9, 4) || '-4' || substr(h, 14, 3) || '-' ||
             substr('89ab', 1 + (abs(random()) % 4), 1) || substr(h, 18, 3) || '-' || substr(h, 21, 12)),
       session_id, path, created_at
FROM (
    SELECT hex(randomblob(16)) AS h, session_id, path, MIN(created_at) AS created_at
    FROM session_artifact_versions
    GROUP BY session_id, path
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS session_artifacts;
-- +goose StatementEnd
