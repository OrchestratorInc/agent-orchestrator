-- name: UpsertReview :exec
INSERT INTO review (id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, harness) DO UPDATE SET
    project_id = excluded.project_id,
    pr_url = excluded.pr_url,
    reviewer_handle_id = excluded.reviewer_handle_id,
    agent_session_id = CASE WHEN excluded.agent_session_id != '' THEN excluded.agent_session_id ELSE review.agent_session_id END,
    reviewer_activity_state = CASE WHEN excluded.reviewer_activity_state != '' THEN excluded.reviewer_activity_state ELSE review.reviewer_activity_state END,
    reviewer_launch_id = CASE WHEN excluded.reviewer_launch_id != '' THEN excluded.reviewer_launch_id ELSE review.reviewer_launch_id END,
	interface_mode = excluded.interface_mode,
	provider_conversation_id = CASE WHEN excluded.provider_conversation_id != '' THEN excluded.provider_conversation_id ELSE review.provider_conversation_id END,
	controller_generation = CASE WHEN excluded.controller_generation != '' THEN excluded.controller_generation ELSE review.controller_generation END,
	controller_error = excluded.controller_error,
    is_archived = excluded.is_archived,
    updated_at = excluded.updated_at;

-- name: GetReviewBySession :one
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at
FROM review WHERE session_id = ? ORDER BY updated_at DESC, created_at DESC, id DESC LIMIT 1;

-- name: GetReviewBySessionAndHarness :one
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at
FROM review WHERE session_id = ? AND harness = ?;

-- name: GetReviewByID :one
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at
FROM review WHERE id = ?;

-- name: ListReviewsBySession :many
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at
FROM review WHERE session_id = ? ORDER BY updated_at DESC, created_at DESC, id DESC;

-- name: ListLiveReviewerHandles :many
-- Every review row that currently owns a live TUI reviewer pane, across the
-- whole daemon: reviewer processes have no session row of their own (their
-- identity is this table's reviewer_handle_id), and a reviewer outlives the
-- worker that spawned it, so this is the only way to find one that survived
-- its worker's death.
SELECT id, session_id, harness, reviewer_handle_id
FROM review WHERE reviewer_handle_id != '';

-- name: SetReviewInterfaceMode :execrows
UPDATE review SET interface_mode = ?, reviewer_handle_id = CASE WHEN ? = 'chat' THEN '' ELSE reviewer_handle_id END,
    provider_conversation_id = CASE WHEN ? = 'tui' THEN '' ELSE provider_conversation_id END,
    controller_generation = CASE WHEN ? = 'chat' THEN '' ELSE controller_generation END,
    controller_error = '', updated_at = ? WHERE id = ?;

-- name: RestoreReviewLaunchState :execrows
UPDATE review SET pr_url = ?, interface_mode = ?, reviewer_handle_id = ?, agent_session_id = ?,
    reviewer_activity_state = ?, reviewer_launch_id = ?, provider_conversation_id = ?,
    controller_generation = ?, controller_error = ?, is_archived = ?, updated_at = ? WHERE id = ?;

-- name: ClaimReviewChatController :execrows
UPDATE review SET provider_conversation_id = ?, controller_generation = ?, controller_error = '', updated_at = ?
WHERE id = ? AND interface_mode = 'chat' AND is_archived = FALSE;

-- name: RecordReviewChatControllerError :execrows
UPDATE review SET controller_error = ?, updated_at = ? WHERE id = ? AND interface_mode = 'chat' AND is_archived = FALSE;

-- name: ClearReviewChatController :execrows
UPDATE review SET controller_generation = '', updated_at = ? WHERE id = ? AND interface_mode = 'chat' AND is_archived = FALSE;

-- name: ListRecoverableChatReviews :many
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, agent_session_id, reviewer_activity_state, reviewer_launch_id, interface_mode, provider_conversation_id, controller_generation, controller_error, is_archived, created_at, updated_at
FROM review WHERE interface_mode = 'chat' AND is_archived = FALSE AND provider_conversation_id != '' ORDER BY updated_at, id;

-- name: ClearReviewerHandle :exec
UPDATE review SET reviewer_handle_id = '', updated_at = CURRENT_TIMESTAMP WHERE session_id = ?;

-- name: ClearReviewerHandleByHarness :exec
UPDATE review SET reviewer_handle_id = '', updated_at = CURRENT_TIMESTAMP WHERE session_id = ? AND harness = ?;

-- name: UpdateReviewAgentSessionID :execrows
UPDATE review SET agent_session_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateReviewActivity :execrows
UPDATE review SET
    agent_session_id = CASE WHEN ? != '' THEN ? ELSE agent_session_id END,
    reviewer_activity_state = CASE WHEN ? != '' THEN ? ELSE reviewer_activity_state END,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND ((? != '' AND reviewer_launch_id = ?)
    OR (? = '' AND reviewer_launch_id = ''));

-- name: InsertReviewRun :exec
INSERT INTO review_run (id, review_id, session_id, batch_id, harness, trigger_source, pr_url, target_sha, status, verdict, body, github_review_id, created_at, auto_inject_review)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: TransitionReviewRun :execrows
-- The one guarded write for a run's state. It changes nothing unless the run
-- is still in one of the allowed from-states, so the first actor to finish a
-- run wins and every later one sees 0 rows and treats it as a no-op.
UPDATE review_run SET
    status = sqlc.arg(to_status),
    verdict = COALESCE(NULLIF(CAST(sqlc.arg(verdict) AS TEXT), ''), verdict),
    body = COALESCE(CAST(sqlc.narg(body) AS TEXT), body),
    github_review_id = COALESCE(CAST(sqlc.narg(github_review_id) AS TEXT), github_review_id),
    auto_inject_review = COALESCE(CAST(sqlc.narg(auto_inject_review) AS BOOLEAN), auto_inject_review),
    failure_reason = COALESCE(NULLIF(CAST(sqlc.arg(failure_reason) AS TEXT), ''), failure_reason),
    cancelled_by = COALESCE(NULLIF(CAST(sqlc.arg(cancelled_by) AS TEXT), ''), cancelled_by),
    launch_id = COALESCE(NULLIF(CAST(sqlc.arg(launch_id) AS TEXT), ''), launch_id),
    launching_at = COALESCE(sqlc.narg(launching_at), launching_at),
    started_at = COALESCE(sqlc.narg(started_at), started_at),
    first_activity_at = COALESCE(sqlc.narg(first_activity_at), first_activity_at),
    cancel_requested_at = COALESCE(sqlc.narg(cancel_requested_at), cancel_requested_at),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at)
WHERE id = sqlc.arg(id) AND status IN (SELECT CAST(value AS TEXT) FROM json_each(CAST(sqlc.arg(from_statuses) AS TEXT)));

-- name: TransitionReviewRunsBySession :many
-- Bulk form of TransitionReviewRun over one worker's runs, optionally scoped to
-- one reviewer harness. It returns the ids it moved.
UPDATE review_run SET
    status = sqlc.arg(to_status),
    verdict = COALESCE(NULLIF(CAST(sqlc.arg(verdict) AS TEXT), ''), verdict),
    body = COALESCE(CAST(sqlc.narg(body) AS TEXT), body),
    github_review_id = COALESCE(CAST(sqlc.narg(github_review_id) AS TEXT), github_review_id),
    auto_inject_review = COALESCE(CAST(sqlc.narg(auto_inject_review) AS BOOLEAN), auto_inject_review),
    failure_reason = COALESCE(NULLIF(CAST(sqlc.arg(failure_reason) AS TEXT), ''), failure_reason),
    cancelled_by = COALESCE(NULLIF(CAST(sqlc.arg(cancelled_by) AS TEXT), ''), cancelled_by),
    launch_id = COALESCE(NULLIF(CAST(sqlc.arg(launch_id) AS TEXT), ''), launch_id),
    launching_at = COALESCE(sqlc.narg(launching_at), launching_at),
    started_at = COALESCE(sqlc.narg(started_at), started_at),
    first_activity_at = COALESCE(sqlc.narg(first_activity_at), first_activity_at),
    cancel_requested_at = COALESCE(sqlc.narg(cancel_requested_at), cancel_requested_at),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at)
WHERE session_id = sqlc.arg(session_id)
  AND (CAST(sqlc.arg(any_harness) AS BOOLEAN) OR harness = sqlc.arg(harness))
  AND status IN (SELECT CAST(value AS TEXT) FROM json_each(CAST(sqlc.arg(from_statuses) AS TEXT)))
RETURNING id;

-- name: TransitionReviewRunsForOlderSHA :many
-- Bulk transition for one PR's runs against any head other than target_sha.
UPDATE review_run SET
    status = sqlc.arg(to_status),
    verdict = COALESCE(NULLIF(CAST(sqlc.arg(verdict) AS TEXT), ''), verdict),
    body = COALESCE(CAST(sqlc.narg(body) AS TEXT), body),
    github_review_id = COALESCE(CAST(sqlc.narg(github_review_id) AS TEXT), github_review_id),
    auto_inject_review = COALESCE(CAST(sqlc.narg(auto_inject_review) AS BOOLEAN), auto_inject_review),
    failure_reason = COALESCE(NULLIF(CAST(sqlc.arg(failure_reason) AS TEXT), ''), failure_reason),
    cancelled_by = COALESCE(NULLIF(CAST(sqlc.arg(cancelled_by) AS TEXT), ''), cancelled_by),
    launch_id = COALESCE(NULLIF(CAST(sqlc.arg(launch_id) AS TEXT), ''), launch_id),
    launching_at = COALESCE(sqlc.narg(launching_at), launching_at),
    started_at = COALESCE(sqlc.narg(started_at), started_at),
    first_activity_at = COALESCE(sqlc.narg(first_activity_at), first_activity_at),
    cancel_requested_at = COALESCE(sqlc.narg(cancel_requested_at), cancel_requested_at),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at)
WHERE session_id = sqlc.arg(session_id)
  AND pr_url = sqlc.arg(pr_url)
  AND target_sha != sqlc.arg(target_sha)
  AND status IN (SELECT CAST(value AS TEXT) FROM json_each(CAST(sqlc.arg(from_statuses) AS TEXT)))
RETURNING id;

-- name: MarkReviewRunsFirstActivity :execrows
-- Records the first activity signal from the launch serving a delivered run.
UPDATE review_run SET first_activity_at = sqlc.arg(at)
WHERE review_id = sqlc.arg(review_id)
  AND launch_id = sqlc.arg(launch_id)
  AND launch_id != ''
  AND status IN ('launching', 'running')
  AND first_activity_at IS NULL;

-- name: GetReviewRun :one
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE id = ?;

-- name: GetReviewRunBySessionPRAndSHA :one
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE session_id = ? AND pr_url = ? AND target_sha = ? ORDER BY created_at DESC LIMIT 1;

-- name: GetReviewRunBySessionPRSHAAndHarness :one
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE session_id = ? AND pr_url = ? AND target_sha = ? AND harness = ? ORDER BY created_at DESC LIMIT 1;

-- name: ListReviewRunsBySession :many
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE session_id = ? ORDER BY created_at DESC;

-- name: ListActiveReviewRunsBySession :many
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE session_id = ? AND status IN ('queued', 'launching', 'running', 'cancelling') ORDER BY created_at DESC;

-- name: ListActiveReviewRuns :many
-- Every active run across the daemon, for the run supervisor and startup sweep.
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE status IN ('queued', 'launching', 'running', 'cancelling') ORDER BY created_at ASC, id ASC;

-- name: ListReviewRunsByBatch :many
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id, delivered_at, batch_id, auto_inject_review, trigger_source, failure_reason, cancelled_by, launch_id, launching_at, started_at, first_activity_at, cancel_requested_at, ended_at
FROM review_run WHERE session_id = ? AND batch_id = ? ORDER BY created_at ASC, id ASC;

-- name: ListCurrentHeadReviewRunsBySession :many
-- AO review passes recorded against each PR's CURRENT head commit. Passes for
-- an earlier head are excluded here so a stale run can never decide the
-- session's Kanban column. The latest same-head pass per (pr, harness) wins,
-- so a superseded retry cannot outvote the rerun that replaced it.
-- failed_auto_runs counts every failed auto pass on that head, the number auto
-- review compares against its retry limit. auto_retry_blocked mirrors the other
-- reasons auto review will not run this head again for this harness: any
-- cancelled pass, any pass with a verdict, even if a later pass superseded it,
-- or a failure auto review must not retry (preflight_failed, cancel_unconfirmed).
SELECT review_run.id, review_run.harness, review_run.pr_url, review_run.status, review_run.verdict, review_run.created_at,
       (
           SELECT COUNT(*)
           FROM review_run failed
           WHERE failed.session_id = review_run.session_id
             AND failed.pr_url = review_run.pr_url
             AND failed.target_sha = review_run.target_sha
             AND failed.status = 'failed'
             AND failed.trigger_source = 'auto'
       ) AS failed_auto_runs,
       CAST(EXISTS (
           SELECT 1
           FROM review_run settled
           WHERE settled.session_id = review_run.session_id
             AND settled.pr_url = review_run.pr_url
             AND settled.target_sha = review_run.target_sha
             AND settled.harness = review_run.harness
             AND (settled.status = 'cancelled'
                  OR settled.verdict IN ('approved', 'changes_requested')
                  OR (settled.status = 'failed' AND settled.failure_reason IN ('preflight_failed', 'cancel_unconfirmed')))
       ) AS INTEGER) AS auto_retry_blocked
FROM review_run
JOIN pr ON pr.url = review_run.pr_url
WHERE review_run.session_id = ?
  AND pr.head_sha != ''
  AND review_run.target_sha = pr.head_sha
  AND NOT EXISTS (
      SELECT 1
      FROM review_run newer
      WHERE newer.session_id = review_run.session_id
        AND newer.pr_url = review_run.pr_url
        AND newer.target_sha = review_run.target_sha
        AND newer.harness = review_run.harness
        AND (
            newer.created_at > review_run.created_at
            OR (newer.created_at = review_run.created_at AND newer.id > review_run.id)
        )
  );

-- name: ListCurrentHeadReviewRunsBySessions :many
-- Batch form of ListCurrentHeadReviewRunsBySession for session-list reads.
-- The latest same-head pass per (session, pr, harness) wins, so the board does
-- not read a superseded retry beside the run that replaced it.
WITH wanted_session AS (
    SELECT CAST(j.value AS TEXT) AS session_id
    FROM json_each(?) AS j
)
SELECT review_run.session_id, review_run.id, review_run.harness, review_run.pr_url, review_run.status, review_run.verdict, review_run.created_at,
       (
           SELECT COUNT(*)
           FROM review_run failed
           WHERE failed.session_id = review_run.session_id
             AND failed.pr_url = review_run.pr_url
             AND failed.target_sha = review_run.target_sha
             AND failed.status = 'failed'
             AND failed.trigger_source = 'auto'
       ) AS failed_auto_runs,
       CAST(EXISTS (
           SELECT 1
           FROM review_run settled
           WHERE settled.session_id = review_run.session_id
             AND settled.pr_url = review_run.pr_url
             AND settled.target_sha = review_run.target_sha
             AND settled.harness = review_run.harness
             AND (settled.status = 'cancelled'
                  OR settled.verdict IN ('approved', 'changes_requested')
                  OR (settled.status = 'failed' AND settled.failure_reason IN ('preflight_failed', 'cancel_unconfirmed')))
       ) AS INTEGER) AS auto_retry_blocked
FROM review_run
JOIN pr ON pr.url = review_run.pr_url
JOIN wanted_session ON wanted_session.session_id = review_run.session_id
WHERE pr.head_sha != ''
  AND review_run.target_sha = pr.head_sha
  AND NOT EXISTS (
      SELECT 1
      FROM review_run newer
      WHERE newer.session_id = review_run.session_id
        AND newer.pr_url = review_run.pr_url
        AND newer.target_sha = review_run.target_sha
        AND newer.harness = review_run.harness
        AND (
            newer.created_at > review_run.created_at
            OR (newer.created_at = review_run.created_at AND newer.id > review_run.id)
        )
  );

-- name: FailUnsubmittedReviewBatchForChatTurn :exec
UPDATE review_run SET status = 'failed', failure_reason = 'turn_ended_without_submit', ended_at = sqlc.arg(ended_at)
WHERE status IN ('launching', 'running') AND batch_id != ''
  AND EXISTS (
    SELECT 1 FROM conversation_turns AS turn
    JOIN conversation_messages AS message ON message.turn_id = turn.id AND message.conversation_id = turn.conversation_id
    JOIN review ON review.id = turn.handled_by_review_id
    WHERE turn.id = sqlc.arg(turn_id)
      AND turn.state IN ('completed', 'recovered', 'failed', 'interrupted', 'cancelled')
      AND turn.handled_by_review_id = review_run.review_id
      AND turn.controller_generation != '' AND turn.controller_generation = review.controller_generation
      AND review.interface_mode = 'chat'
      AND message.role = 'user' AND message.origin = 'daemon'
      AND message.client_message_id = 'review-batch:' || review_run.batch_id
  );

-- name: ArchiveReviewsBySession :exec
UPDATE review SET is_archived = TRUE, reviewer_handle_id = '', reviewer_launch_id = '',
    controller_generation = '', reviewer_activity_state = 'exited', updated_at = ? WHERE session_id = ?;
