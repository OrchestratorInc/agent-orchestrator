-- +goose Up
-- A review run started with `ao review trigger --no-inject` must never send
-- its own review back into the session that started it, whatever the session's
-- live auto-inject setting is when the result arrives (#6810).
ALTER TABLE review_run ADD COLUMN no_inject BOOLEAN NOT NULL DEFAULT FALSE;
-- A PR attached only so a --no-inject review could run on it. The session that
-- started the review does not own the PR, so it gets no CI, review, or
-- merge-conflict nudges for it (#6810).
ALTER TABLE pr ADD COLUMN review_only BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE pr DROP COLUMN review_only;
ALTER TABLE review_run DROP COLUMN no_inject;
