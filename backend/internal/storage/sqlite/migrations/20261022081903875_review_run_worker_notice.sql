-- +goose Up
-- +goose StatementBegin
-- reviewer_model records the model the reviewer ran with for this pass ('' =
-- the harness default), so the completion notice can name it.
ALTER TABLE review_run ADD COLUMN reviewer_model TEXT NOT NULL DEFAULT '';
-- worker_notice tracks the one "review done" message AO sends the worker when
-- a pass reaches a final state: '' = none owed (legacy rows, or review
-- feedback delivery off), 'pending' = owed, 'sent' = delivered.
ALTER TABLE review_run ADD COLUMN worker_notice TEXT NOT NULL DEFAULT ''
    CHECK (worker_notice IN ('', 'pending', 'sent'));
CREATE INDEX idx_review_run_worker_notice_pending
    ON review_run (session_id, created_at)
    WHERE worker_notice = 'pending';
-- Every running -> final transition owes the worker one notice when its
-- session receives review feedback. Marking it here, atomically with the
-- status write, covers every path that ends a pass (submit, cancel,
-- supersede, reviewer failure) and survives a daemon restart.
CREATE TRIGGER review_run_worker_notice_pending
AFTER UPDATE OF status ON review_run
WHEN OLD.status = 'running'
    AND NEW.status IN ('complete', 'failed', 'cancelled')
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS review_run_worker_notice_pending;
DROP INDEX IF EXISTS idx_review_run_worker_notice_pending;
ALTER TABLE review_run DROP COLUMN worker_notice;
ALTER TABLE review_run DROP COLUMN reviewer_model;
-- +goose StatementEnd
