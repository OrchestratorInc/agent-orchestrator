-- +goose Up
-- +goose StatementBegin
ALTER TABLE session_artifact_versions
    ADD COLUMN source TEXT NOT NULL DEFAULT 'agent' CHECK (source IN ('agent', 'user'));
ALTER TABLE session_artifact_versions
    ADD COLUMN harness TEXT NOT NULL DEFAULT '';
ALTER TABLE session_artifact_versions
    ADD COLUMN restored_from INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE session_artifact_versions DROP COLUMN restored_from;
ALTER TABLE session_artifact_versions DROP COLUMN harness;
ALTER TABLE session_artifact_versions DROP COLUMN source;
-- +goose StatementEnd
