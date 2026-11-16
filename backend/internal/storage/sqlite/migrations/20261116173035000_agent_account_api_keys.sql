-- +goose Up
-- An account is a subscription sign-in or an Anthropic API key. An API key
-- account is a config folder like any other, and its key is kept in a 0600
-- file in that folder, never here; key_hint is the key's last four characters,
-- for telling keys apart. name is the label the user gave an API key account.
ALTER TABLE agent_accounts ADD COLUMN kind TEXT NOT NULL DEFAULT 'subscription' CHECK (kind IN ('subscription', 'api_key'));
ALTER TABLE agent_accounts ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN key_hint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN key_hint;
ALTER TABLE agent_accounts DROP COLUMN name;
ALTER TABLE agent_accounts DROP COLUMN kind;
