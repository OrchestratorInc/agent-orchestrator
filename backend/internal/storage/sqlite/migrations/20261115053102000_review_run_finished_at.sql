-- +goose Up
-- When a review run left 'running'. A --no-inject pass that ended without
-- recording its provider review id still owns whatever its reviewer posted
-- while it ran, so the SCM observer needs the end of that window (#6810).
ALTER TABLE review_run ADD COLUMN finished_at TIMESTAMP;
-- Runs that already ended have no recorded end; their start is the closest
-- bound, so none of them reads as still running.
UPDATE review_run SET finished_at = created_at WHERE status != 'running';

-- +goose Down
ALTER TABLE review_run DROP COLUMN finished_at;
