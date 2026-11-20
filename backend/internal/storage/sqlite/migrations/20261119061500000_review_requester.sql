-- +goose Up
-- +goose StatementBegin
ALTER TABLE review_run ADD COLUMN requester_session_id TEXT NOT NULL DEFAULT '';
-- Older runs did not record their caller; avoid routing their pending notices to the PR owner.
UPDATE review_run SET worker_notice = '' WHERE worker_notice = 'pending';
DROP TRIGGER review_run_worker_notice_pending;
CREATE TRIGGER review_run_worker_notice_pending
AFTER UPDATE OF status ON review_run
WHEN OLD.status IN ('queued', 'launching', 'running', 'cancelling')
    AND NEW.status IN ('completed', 'failed', 'cancelled', 'superseded')
    AND NEW.worker_notice = ''
    AND NEW.trigger_source = 'agent' AND NEW.requester_session_id <> ''
    -- Synchronous trigger failures are returned directly to the requesting CLI.
    AND NOT (NEW.status = 'failed' AND NEW.failure_reason IN ('preflight_failed', 'launch_failed')
             AND OLD.status IN ('queued', 'launching') AND OLD.started_at IS NULL)
BEGIN
    UPDATE review_run SET worker_notice = 'pending'
    WHERE id = NEW.id
      AND EXISTS (
          SELECT 1 FROM sessions
          WHERE sessions.id = NEW.requester_session_id
            AND sessions.project_id = (SELECT project_id FROM sessions WHERE id = NEW.session_id)
            AND (SELECT auto_inject_review FROM sessions WHERE id = NEW.session_id)
            AND NOT sessions.is_terminated
      );
END;
CREATE INDEX idx_review_run_requester_notice ON review_run(requester_session_id, created_at) WHERE worker_notice = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_review_run_requester_notice;
DROP TRIGGER review_run_worker_notice_pending;
ALTER TABLE review_run DROP COLUMN requester_session_id;
CREATE TRIGGER review_run_worker_notice_pending
AFTER UPDATE OF status ON review_run
WHEN OLD.status IN ('queued', 'launching', 'running', 'cancelling')
    AND NEW.status IN ('completed', 'failed', 'cancelled', 'superseded')
    AND NEW.worker_notice = ''
BEGIN
    UPDATE review_run SET worker_notice = 'pending'
    WHERE id = NEW.id
      AND EXISTS (
          SELECT 1 FROM sessions
          WHERE sessions.id = NEW.session_id
            AND sessions.auto_inject_review
            AND NOT sessions.is_terminated
      );
END;
-- +goose StatementEnd
