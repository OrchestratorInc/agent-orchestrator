-- +goose Up
-- The name and color of each provider's "This computer" account, which has no
-- agent_accounts row. Keyed by its account id (device-claude, device-codex).
CREATE TABLE device_agent_account_identities (
    account_id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    color TEXT
);

-- +goose Down
DROP TABLE device_agent_account_identities;
