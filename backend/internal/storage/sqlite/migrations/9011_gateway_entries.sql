-- +goose Up
-- +goose StatementBegin
CREATE TABLE gateway_entries (
    scope TEXT NOT NULL CHECK (scope IN ('app', 'project')),
    project_id TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    auth_token TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (scope, project_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gateway_entries;
-- +goose StatementEnd
