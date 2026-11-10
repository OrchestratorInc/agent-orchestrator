-- +goose Up
ALTER TABLE sessions ADD COLUMN independent_orchestrator INTEGER NOT NULL DEFAULT 0 CHECK (independent_orchestrator IN (0, 1));
ALTER TABLE sessions ADD COLUMN orchestrator_session_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN orchestrator_session_id;
ALTER TABLE sessions DROP COLUMN independent_orchestrator;
