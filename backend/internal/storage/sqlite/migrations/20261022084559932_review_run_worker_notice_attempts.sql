-- +goose Up
-- +goose StatementBegin
-- The last failed attempt to deliver a pass's "review done" message: when it
-- happened, how many attempts failed, and the send error. A failed send keeps
-- the notice pending; these columns drive its retry backoff and show why the
-- worker's session refused it.
ALTER TABLE review_run ADD COLUMN worker_notice_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE review_run ADD COLUMN worker_notice_error TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN worker_notice_attempted_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE review_run DROP COLUMN worker_notice_attempted_at;
ALTER TABLE review_run DROP COLUMN worker_notice_error;
ALTER TABLE review_run DROP COLUMN worker_notice_attempts;
-- +goose StatementEnd
