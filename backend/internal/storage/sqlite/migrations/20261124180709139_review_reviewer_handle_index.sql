-- +goose Up
CREATE INDEX idx_review_reviewer_handle ON review(reviewer_handle_id) WHERE reviewer_handle_id <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_review_reviewer_handle;
