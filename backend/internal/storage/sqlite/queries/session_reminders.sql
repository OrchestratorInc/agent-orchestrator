-- One-shot follow-ups an agent schedules into its own session (ao remind).
-- Rows only move out of 'pending' once; a guarded status check keeps a
-- cancel and a delivery from both winning.

-- name: InsertSessionReminder :exec
INSERT INTO session_reminders (
    id, session_id, project_id, name, message, due_at, created_by,
    status, status_reason, attempts, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', '', 0, ?, ?);

-- name: SelectSessionReminderByID :one
SELECT *
FROM session_reminders
WHERE id = ?;

-- name: SelectPendingSessionReminders :many
SELECT *
FROM session_reminders
WHERE session_id = ? AND status = 'pending'
ORDER BY due_at, id;

-- name: CountPendingSessionReminders :one
SELECT COUNT(*)
FROM session_reminders
WHERE session_id = ? AND status = 'pending';

-- name: SelectDueSessionReminders :many
SELECT *
FROM session_reminders
WHERE status = 'pending' AND due_at <= ?
ORDER BY due_at, id
LIMIT ?;

-- name: ResolveSessionReminder :execrows
UPDATE session_reminders
SET status = ?, status_reason = ?, resolved_at = ?, updated_at = ?
WHERE id = ? AND status = 'pending';

-- name: DeferSessionReminder :execrows
UPDATE session_reminders
SET due_at = ?, status_reason = ?, attempts = attempts + 1, updated_at = ?
WHERE id = ? AND status = 'pending';

-- Delivered agent reminders since the session last received a human-authored
-- message: the streak an agent builds by checking on its own.
-- name: CountRemindersDeliveredSinceHumanMessage :one
SELECT COUNT(*)
FROM session_reminders AS reminder
WHERE reminder.session_id = sqlc.arg(session_id)
  AND reminder.status = 'delivered'
  AND reminder.created_by = 'agent'
  AND reminder.resolved_at > COALESCE((
      SELECT message.created_at
      FROM conversation_messages AS message
      JOIN conversations AS conversation ON conversation.id = message.conversation_id
      WHERE (conversation.session_id = sqlc.arg(session_id) OR conversation.current_session_id = sqlc.arg(session_id))
        AND message.role = 'user'
        AND message.origin = 'human'
      ORDER BY message.created_at DESC
      LIMIT 1
      -- No human message yet: every delivered reminder counts.
  ), '');
