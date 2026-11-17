-- Review runs become an explicit state machine (queued -> launching -> running
-- -> completed, with cancelling, failed, cancelled and superseded). Each final
-- state records why it ended in its own column, so body goes back to meaning
-- the submitted review text only.

-- +goose Up
ALTER TABLE review_run ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN cancelled_by TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN launch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN launching_at TIMESTAMP;
ALTER TABLE review_run ADD COLUMN started_at TIMESTAMP;
ALTER TABLE review_run ADD COLUMN first_activity_at TIMESTAMP;
ALTER TABLE review_run ADD COLUMN cancel_requested_at TIMESTAMP;
ALTER TABLE review_run ADD COLUMN ended_at TIMESTAMP;

-- +goose StatementBegin
DROP INDEX idx_review_run_session_pr_sha_harness;
-- +goose StatementEnd

UPDATE review_run SET status = 'completed' WHERE status IN ('complete', 'delivered');

UPDATE review_run SET status = 'superseded', body = ''
WHERE status = 'failed' AND body = 'superseded by a review trigger for a newer commit';

UPDATE review_run
SET failure_reason = CASE
        WHEN body = 'reviewer process exited before submitting a result' THEN 'process_exited'
        WHEN body = 'reviewer Chat turn ended without submitting a result' THEN 'turn_ended_without_submit'
        WHEN body LIKE 'reviewer preflight:%' THEN 'preflight_failed'
        ELSE 'launch_failed'
    END,
    body = ''
WHERE status = 'failed';

UPDATE review_run
SET cancelled_by = CASE WHEN body = 'cancelled by user' THEN 'user' ELSE 'worker_lifecycle' END,
    body = ''
WHERE status = 'cancelled';

-- Running rows stay running: the daemon's startup sweep keeps the ones whose
-- reviewer it can re-adopt and fails the rest. Give them the launch that was
-- serving them and treat them as already started, since that is unknowable now.
UPDATE review_run
SET launch_id = COALESCE((SELECT review.reviewer_launch_id FROM review WHERE review.id = review_run.review_id), ''),
    started_at = created_at,
    first_activity_at = created_at
WHERE status = 'running';

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status IN ('queued', 'launching', 'running', 'cancelling');
-- +goose StatementEnd

CREATE INDEX idx_review_run_active_status
    ON review_run (status)
    WHERE status IN ('queued', 'launching', 'running', 'cancelling');

-- +goose Down
DROP INDEX idx_review_run_active_status;

-- +goose StatementBegin
DROP INDEX idx_review_run_session_pr_sha_harness;
-- +goose StatementEnd

UPDATE review_run SET status = 'complete' WHERE status = 'completed';
UPDATE review_run SET status = 'failed', body = 'superseded by a review trigger for a newer commit' WHERE status = 'superseded';
UPDATE review_run SET status = 'cancelled' WHERE status = 'cancelling';
UPDATE review_run SET status = 'failed' WHERE status IN ('queued', 'launching');
UPDATE review_run
SET body = CASE failure_reason
        WHEN 'process_exited' THEN 'reviewer process exited before submitting a result'
        WHEN 'turn_ended_without_submit' THEN 'reviewer Chat turn ended without submitting a result'
        ELSE failure_reason
    END
WHERE status = 'failed' AND body = '';
UPDATE review_run
SET body = CASE cancelled_by WHEN 'user' THEN 'cancelled by user' ELSE 'cancelled by worker session lifecycle' END
WHERE status = 'cancelled' AND body = '';

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status = 'running';
-- +goose StatementEnd

ALTER TABLE review_run DROP COLUMN ended_at;
ALTER TABLE review_run DROP COLUMN cancel_requested_at;
ALTER TABLE review_run DROP COLUMN first_activity_at;
ALTER TABLE review_run DROP COLUMN started_at;
ALTER TABLE review_run DROP COLUMN launching_at;
ALTER TABLE review_run DROP COLUMN launch_id;
ALTER TABLE review_run DROP COLUMN cancelled_by;
ALTER TABLE review_run DROP COLUMN failure_reason;
