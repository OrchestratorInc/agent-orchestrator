-- +goose Up
-- The paths inside the session's artifact directory that ao artifact add
-- registered as deliverables, as a JSON array of strings.
ALTER TABLE sessions ADD COLUMN registered_artifact_paths TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE sessions DROP COLUMN registered_artifact_paths;
