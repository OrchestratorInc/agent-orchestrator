-- +goose Up
CREATE INDEX idx_sessions_preview_poll
ON sessions (project_id, num)
WHERE is_terminated = 0 AND kind = 'worker';

-- +goose Down
DROP INDEX idx_sessions_preview_poll;
