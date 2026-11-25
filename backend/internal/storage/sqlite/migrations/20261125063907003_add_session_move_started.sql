-- +goose Up
-- The move id of a move to AO Cloud whose export pushed this session's branch
-- and has not finished or been rolled back; '' otherwise. Restoring at startup
-- leaves such a session stopped, so it cannot run here while the move may
-- have started it in AO Cloud.
ALTER TABLE sessions ADD COLUMN move_started TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN move_started;
