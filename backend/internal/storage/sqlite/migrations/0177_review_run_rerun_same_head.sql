-- An explicit re-review of a commit must be able to run again. The previous key
-- also covered approved passes, so a second pass by the same reviewer on an
-- already-approved commit collided with the first and was silently reused, and
-- `ao review trigger --rerun` could never review that commit again. Keep only
-- the guard the index exists for: one running pass per (worker, PR, commit,
-- reviewer), which still blocks the concurrent double-spawn race (#242). Whether
-- an already-reviewed commit should be reviewed again is the engine's decision.

-- +goose Up
-- +goose StatementBegin
DROP INDEX idx_review_run_session_pr_sha_harness;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_review_run_session_pr_sha_harness;
-- +goose StatementEnd

-- Collapse each key to one row the wider index accepts before restoring it,
-- or it would fail on repeat approvals (and an approval beside a running
-- rerun) that this migration made legal. Keep a completed pass over a running
-- one, then the newest, as 0042 does.
-- +goose StatementBegin
DELETE FROM review_run
WHERE rowid IN (
  SELECT rowid FROM (
    SELECT rowid,
           ROW_NUMBER() OVER (
             PARTITION BY session_id, pr_url, target_sha, harness
             ORDER BY CASE status WHEN 'running' THEN 1 ELSE 0 END,
                      created_at DESC,
                      rowid DESC
           ) AS rn
    FROM review_run
    WHERE target_sha != ''
      AND status NOT IN ('failed', 'cancelled')
      AND (status = 'running' OR verdict NOT IN ('', 'changes_requested'))
  )
  WHERE rn > 1
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status NOT IN ('failed', 'cancelled')
        AND (status = 'running' OR verdict NOT IN ('', 'changes_requested'));
-- +goose StatementEnd
