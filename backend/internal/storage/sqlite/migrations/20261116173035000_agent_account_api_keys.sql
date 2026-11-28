-- +goose Up
-- An account is a subscription sign-in, a subscription setup token, an
-- Anthropic API key, or a metered (per-token) sign-in such as Anthropic
-- Console. Every account is a config folder; a saved key or token is kept in a
-- 0600 file in that folder, never here. key_hint is a key's last four
-- characters, for telling keys apart; name is the label the user gave an
-- account that has no email of its own.
ALTER TABLE agent_accounts ADD COLUMN kind TEXT NOT NULL DEFAULT 'subscription' CHECK (kind IN ('subscription', 'setup_token', 'api_key', 'metered'));
ALTER TABLE agent_accounts ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN key_hint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN key_hint;
ALTER TABLE agent_accounts DROP COLUMN name;
ALTER TABLE agent_accounts DROP COLUMN kind;
