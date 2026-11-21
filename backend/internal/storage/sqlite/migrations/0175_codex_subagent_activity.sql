-- +goose Up
-- Codex child hook facts are scoped to the runtime launch. The existing
-- activity_state remains the lifecycle projection.
ALTER TABLE sessions ADD COLUMN codex_activity_facts TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN codex_activity_facts;
