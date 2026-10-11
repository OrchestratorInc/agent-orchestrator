-- +goose Up
-- Provider hook facts, scoped to the Claude runtime launch. The visible
-- activity_state remains the existing lifecycle projection.
ALTER TABLE sessions ADD COLUMN claude_activity_facts TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN claude_activity_facts;
