-- +goose Up
-- Keep startup timestamp detection off large activity payload pages.
CREATE INDEX idx_conversation_activities_timestamps ON conversation_activities (created_at, updated_at);

-- +goose Down
DROP INDEX idx_conversation_activities_timestamps;
