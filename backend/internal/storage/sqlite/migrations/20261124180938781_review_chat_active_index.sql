-- +goose Up
CREATE INDEX idx_review_chat_active ON review(interface_mode, is_archived, updated_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_review_chat_active;
