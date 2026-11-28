-- +goose Up
CREATE INDEX idx_review_run_github_review_id ON review_run(github_review_id) WHERE github_review_id <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_review_run_github_review_id;
