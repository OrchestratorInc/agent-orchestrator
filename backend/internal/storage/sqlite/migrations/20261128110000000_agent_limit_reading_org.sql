-- +goose Up
-- The organization a reading's login belongs to, so two logins that share an
-- email address (a personal plan and a team seat) keep separate limit history.
ALTER TABLE agent_limit_readings ADD COLUMN org_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_limit_readings DROP COLUMN org_id;
