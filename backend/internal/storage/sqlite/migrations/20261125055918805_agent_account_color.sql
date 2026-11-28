-- +goose Up
-- The palette id an account is tinted with in the app; NULL means no color.
ALTER TABLE agent_accounts ADD COLUMN color TEXT;

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN color;
