-- +goose Up
CREATE INDEX idx_review_run_current_head_lookup
ON review_run (session_id, pr_url, target_sha, harness, created_at, id);

-- +goose Down
DROP INDEX idx_review_run_current_head_lookup;
