-- name: InsertAutomation :exec
INSERT INTO automations (
    id, project_id, display_name, prompt, kind, harness, rrule_text,
    timezone, enabled, next_run_at, last_run_at, created_at, updated_at, model, mode, effort
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAutomation :one
SELECT id, project_id, display_name, prompt, kind, harness, rrule_text,
    timezone, enabled, next_run_at, last_run_at, created_at, updated_at, model, mode, effort
FROM automations
WHERE id = ?;

-- name: ListAutomations :many
SELECT id, project_id, display_name, prompt, kind, harness, rrule_text,
    timezone, enabled, next_run_at, last_run_at, created_at, updated_at, model, mode, effort
FROM automations
WHERE (sqlc.arg(project_id) = '' OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(enabled_filter) < 0 OR enabled = sqlc.arg(enabled_filter))
ORDER BY created_at, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAutomations :one
SELECT COUNT(*)
FROM automations
WHERE (sqlc.arg(project_id) = '' OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(enabled_filter) < 0 OR enabled = sqlc.arg(enabled_filter));

-- name: UpdateAutomation :execrows
UPDATE automations
SET display_name = ?, prompt = ?, kind = ?, harness = ?, model = ?, mode = ?, effort = ?, rrule_text = ?,
    timezone = ?, enabled = ?, next_run_at = ?, last_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteAutomation :execrows
DELETE FROM automations WHERE id = ?;

-- name: AdvanceAutomationSchedule :execrows
UPDATE automations
SET last_run_at = ?, next_run_at = ?, updated_at = ?
WHERE id = ? AND enabled = 1 AND next_run_at = ?;

-- name: ListDueAutomations :many
SELECT id, project_id, display_name, prompt, kind, harness, rrule_text,
    timezone, enabled, next_run_at, last_run_at, created_at, updated_at, model, mode, effort
FROM automations
WHERE enabled = 1 AND next_run_at <= ?
ORDER BY next_run_at, id
LIMIT ?;

-- name: InsertAutomationRun :execrows
INSERT INTO automation_runs (
    id, automation_id, scheduled_for, session_id, status, attempt_count,
    claimed_at, lease_expires_at, started_at, finished_at, error_message,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (automation_id, scheduled_for) DO NOTHING;

-- name: GetAutomationRun :one
SELECT id, automation_id, scheduled_for, session_id, status, attempt_count,
    claimed_at, lease_expires_at, started_at, finished_at, error_message,
    created_at, updated_at
FROM automation_runs
WHERE id = ?;

-- name: ListActiveAutomationRuns :many
SELECT id, automation_id, scheduled_for, session_id, status, attempt_count,
    claimed_at, lease_expires_at, started_at, finished_at, error_message,
    created_at, updated_at
FROM automation_runs
WHERE status IN ('spawning', 'running')
ORDER BY scheduled_for, id;

-- name: GetAutomationRunByOccurrence :one
SELECT id, automation_id, scheduled_for, session_id, status, attempt_count,
    claimed_at, lease_expires_at, started_at, finished_at, error_message,
    created_at, updated_at
FROM automation_runs
WHERE automation_id = ? AND scheduled_for = ?;

-- name: ListAutomationRuns :many
SELECT id, automation_id, scheduled_for, session_id, status, attempt_count,
    claimed_at, lease_expires_at, started_at, finished_at, error_message,
    created_at, updated_at
FROM automation_runs
WHERE automation_id = sqlc.arg(automation_id)
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY scheduled_for DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAutomationRuns :one
SELECT COUNT(*)
FROM automation_runs
WHERE automation_id = sqlc.arg(automation_id)
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter));

-- name: ListLatestAutomationRuns :many
SELECT r.id, r.automation_id, r.scheduled_for, r.session_id, r.status,
    r.attempt_count, r.claimed_at, r.lease_expires_at, r.started_at,
    r.finished_at, r.error_message, r.created_at, r.updated_at
FROM automation_runs r
WHERE r.automation_id IN (sqlc.slice('automation_ids'))
  AND r.id = (
      SELECT newest.id
      FROM automation_runs newest
      WHERE newest.automation_id = r.automation_id
      ORDER BY newest.scheduled_for DESC, newest.id DESC
      LIMIT 1
  )
ORDER BY r.automation_id;

-- name: GetNextClaimableAutomationRun :one
SELECT r.id, r.automation_id, r.scheduled_for, r.session_id, r.status,
    r.attempt_count, r.claimed_at, r.lease_expires_at, r.started_at,
    r.finished_at, r.error_message, r.created_at, r.updated_at
FROM automation_runs r
JOIN automations a ON a.id = r.automation_id
WHERE r.status = 'pending'
  AND a.enabled = 1
  AND NOT EXISTS (
      SELECT 1 FROM automation_runs active
      WHERE active.automation_id = r.automation_id
        AND active.status IN ('spawning', 'running')
  )
ORDER BY r.scheduled_for, r.id
LIMIT 1;

-- name: ClaimAutomationRun :execrows
UPDATE automation_runs
SET status = 'spawning', attempt_count = attempt_count + 1,
    claimed_at = ?, lease_expires_at = ?, error_message = NULL, updated_at = ?
WHERE automation_runs.id = ? AND automation_runs.status = 'pending'
  AND NOT EXISTS (
      SELECT 1 FROM automation_runs active
      WHERE active.automation_id = automation_runs.automation_id
        AND active.id <> automation_runs.id
        AND active.status IN ('spawning', 'running')
  );

-- name: MarkAutomationRunRunning :execrows
UPDATE automation_runs
SET status = 'running', session_id = ?, started_at = ?,
    lease_expires_at = NULL, error_message = NULL, updated_at = ?
WHERE id = ? AND status = 'spawning';

-- name: CompleteAutomationRun :execrows
UPDATE automation_runs
SET status = 'completed', finished_at = ?, claimed_at = NULL,
    lease_expires_at = NULL, error_message = NULL, updated_at = ?
WHERE id = ? AND status = 'running';

-- name: FailAutomationRun :execrows
UPDATE automation_runs
SET status = 'failed', finished_at = ?, claimed_at = NULL,
    lease_expires_at = NULL, error_message = ?, updated_at = ?
WHERE id = ? AND status IN ('pending', 'spawning', 'running');

-- name: ReleaseAutomationRun :execrows
UPDATE automation_runs
SET status = 'pending', claimed_at = NULL, lease_expires_at = NULL,
    error_message = ?, updated_at = ?
WHERE id = ? AND status = 'spawning';

-- Preserve stale queued occurrences in history, without replaying obsolete work.
-- name: SupersedePendingAutomationRuns :exec
UPDATE automation_runs
SET status = 'failed', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now),
    claimed_at = NULL, lease_expires_at = NULL,
    error_message = 'Superseded by a newer scheduled occurrence'
WHERE status = 'pending' AND scheduled_for <= sqlc.arg(now)
  AND EXISTS (
      SELECT 1 FROM automation_runs newer
      WHERE newer.automation_id = automation_runs.automation_id
        AND newer.scheduled_for > automation_runs.scheduled_for
        AND newer.scheduled_for <= sqlc.arg(now)
  );

-- Idle alone is not completion: a successful durable turn (Chat) or a settled
-- done report (TUI) is required, with no outstanding work or open PR.
-- name: AutomationSessionReadyToComplete :one
SELECT EXISTS (
    SELECT 1 FROM sessions s
    WHERE s.id = sqlc.arg(session_id) AND s.kind = 'worker'
      AND s.is_terminated = 0 AND s.activity_state = 'idle'
      AND NOT EXISTS (
          SELECT 1 FROM reports r
          WHERE r.id = (SELECT id FROM reports WHERE session_id = s.id ORDER BY created_at DESC, rowid DESC LIMIT 1)
            AND r.state IN ('needs_input', 'stuck')
      )
      AND NOT EXISTS (SELECT 1 FROM pr WHERE session_id = s.id AND pr_state IN ('open', 'draft'))
      AND NOT EXISTS (
          SELECT 1 FROM conversation_turns t
          JOIN conversations c ON c.id = t.conversation_id
          WHERE (t.handled_by_session_id = s.id OR c.session_id = s.id OR c.current_session_id = s.id)
            AND t.state IN ('queued', 'running')
      )
      AND (
          (s.session_mode = 'chat' AND (
              SELECT t.state FROM conversation_turns t
              JOIN conversations c ON c.id = t.conversation_id
              WHERE t.handled_by_session_id = s.id AND c.scope = 'session'
                AND t.rolled_back_at IS NULL AND t.promoted_to_turn_id IS NULL
                AND t.branch_id = c.active_branch_id
              ORDER BY t.requested_at DESC, t.rowid DESC LIMIT 1
          ) = 'completed')
          OR (s.session_mode = 'tui' AND EXISTS (
              SELECT 1 FROM reports r
              WHERE r.id = (SELECT id FROM reports WHERE session_id = s.id ORDER BY created_at DESC, rowid DESC LIMIT 1)
                AND r.state = 'done' AND r.settlement_deadline IS NOT NULL
                AND r.settlement_deadline <= sqlc.arg(now)
                AND r.created_at >= s.created_at
                AND (s.latest_user_prompt_at IS NULL OR r.created_at >= s.latest_user_prompt_at)
          ))
      )
);
