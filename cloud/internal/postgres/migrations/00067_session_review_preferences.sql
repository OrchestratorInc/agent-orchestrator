-- +goose Up

-- Cloud session controls mirror the desktop inspector settings. Reviewer
-- harness is empty when the review should use the worker session's harness.
-- auto_inject_ci, auto_inject_review and terminate_on_pr_merge already exist
-- (00046/00047), so this migration only adds the reviewer selection.
ALTER TABLE ao_sessions
	ADD COLUMN IF NOT EXISTS reviewer_harness TEXT NOT NULL DEFAULT '';

-- Keep the provider which actually executed a review. A session's reviewer
-- setting is mutable, so deriving historic runs from the current setting would
-- show the wrong agent.
ALTER TABLE ao_review_runs
	ADD COLUMN IF NOT EXISTS harness TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE ao_review_runs DROP COLUMN IF EXISTS harness;
ALTER TABLE ao_sessions DROP COLUMN IF EXISTS reviewer_harness;
