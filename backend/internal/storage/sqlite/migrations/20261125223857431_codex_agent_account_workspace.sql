-- +goose Up
ALTER TABLE agent_accounts ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN workspace_id;
