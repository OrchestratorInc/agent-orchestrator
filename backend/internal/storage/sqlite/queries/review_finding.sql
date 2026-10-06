-- name: InsertReviewFinding :exec
INSERT INTO review_finding (id, run_id, session_id, pr_url, target_sha, ordinal, path, line, body, status, superseded_by_run_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindSupersedingReviewRun :one
-- The earliest pass that already completed on the same PR and replaces the
-- given one under the same rule as SupersedeOpenReviewFindings: it started
-- later and reviewed a different head or used the same reviewer. A pass that
-- finishes after such a pass files its findings as already superseded.
SELECT newer.id
FROM review_run newer
WHERE newer.session_id = sqlc.arg(session_id)
  AND newer.pr_url = sqlc.arg(pr_url)
  AND newer.id != sqlc.arg(run_id)
  AND newer.status IN ('complete', 'delivered')
  AND (newer.created_at > sqlc.arg(created_at)
       OR (newer.created_at = sqlc.arg(created_at) AND newer.id > sqlc.arg(run_id)))
  AND (newer.target_sha != sqlc.arg(target_sha) OR newer.harness = sqlc.arg(harness))
ORDER BY newer.created_at, newer.id
LIMIT 1;

-- name: SupersedeOpenReviewFindings :execrows
-- A completed pass replaces the open findings of every earlier pass on the same
-- PR, except a different reviewer's pass on the same head: parallel reviewers
-- of one commit each keep their own findings. A pass never supersedes one that
-- started after it.
UPDATE review_finding
SET status = 'superseded', superseded_by_run_id = sqlc.arg(new_run_id)
WHERE review_finding.session_id = sqlc.arg(session_id)
  AND review_finding.pr_url = sqlc.arg(pr_url)
  AND review_finding.status = 'open'
  AND review_finding.run_id != sqlc.arg(new_run_id)
  AND EXISTS (
      SELECT 1 FROM review_run older
      WHERE older.id = review_finding.run_id
        AND (older.created_at < sqlc.arg(created_at)
             OR (older.created_at = sqlc.arg(created_at) AND older.id < sqlc.arg(new_run_id)))
        AND (older.target_sha != sqlc.arg(target_sha) OR older.harness = sqlc.arg(harness))
  );

-- name: ListReviewFindingsBySession :many
SELECT id, run_id, session_id, pr_url, target_sha, ordinal, path, line, body, status, resolution_note, resolved_by_session_id, resolved_at, superseded_by_run_id, created_at
FROM review_finding WHERE session_id = ? ORDER BY created_at, run_id, ordinal;

-- name: ListReviewFindingsByRun :many
SELECT id, run_id, session_id, pr_url, target_sha, ordinal, path, line, body, status, resolution_note, resolved_by_session_id, resolved_at, superseded_by_run_id, created_at
FROM review_finding WHERE run_id = ? ORDER BY ordinal;

-- name: GetReviewFinding :one
SELECT id, run_id, session_id, pr_url, target_sha, ordinal, path, line, body, status, resolution_note, resolved_by_session_id, resolved_at, superseded_by_run_id, created_at
FROM review_finding WHERE id = ?;

-- name: ResolveReviewFinding :execrows
UPDATE review_finding
SET status = 'resolved', resolution_note = ?, resolved_by_session_id = ?, resolved_at = ?
WHERE id = ? AND status = 'open';

-- name: SetReviewRunProviderPost :execrows
UPDATE review_run SET github_review_id = ?, provider_post_error = ? WHERE id = ?;

-- name: MarkReviewRunDelivered :execrows
UPDATE review_run SET status = 'delivered', delivered_at = ? WHERE id = ? AND status = 'complete' AND delivered_at IS NULL;

-- name: ListUndeliveredReviewRunsForPR :many
-- Completed AO review passes for a PR that the worker has not been told about
-- yet. Callers still fence on the PR's current head.
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, provider_post_error, delivery_skipped_reason
FROM review_run
WHERE session_id = ? AND pr_url = ? AND status = 'complete' AND delivered_at IS NULL AND delivery_skipped_reason = '' AND verdict != '' AND auto_inject_review
ORDER BY created_at, id;

-- name: ListUndeliveredReviewRuns :many
-- Every worker's completed, undelivered passes, for the delivery sweep that
-- retries a delivery the worker could not take when its review finished.
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, provider_post_error, delivery_skipped_reason
FROM review_run
WHERE status = 'complete' AND delivered_at IS NULL AND delivery_skipped_reason = '' AND verdict != '' AND auto_inject_review
ORDER BY created_at, id;

-- name: RetireReviewRunDelivery :execrows
UPDATE review_run SET delivery_skipped_reason = ?
WHERE id = ? AND status = 'complete' AND delivered_at IS NULL AND delivery_skipped_reason = '';
