-- Subscription accounts AO launches Claude Code and Codex with.

-- name: ListAgentAccounts :many
SELECT * FROM agent_accounts ORDER BY provider, created_at, id;

-- name: CreateAgentAccount :exec
INSERT INTO agent_accounts (id, provider, home, email, plan, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteAgentAccount :exec
DELETE FROM agent_accounts WHERE id = ?;

-- name: DeleteAgentAccountDefaultFor :exec
DELETE FROM agent_account_defaults WHERE account_id = ?;

-- name: GetAgentAccountDefault :one
SELECT account_id FROM agent_account_defaults WHERE provider = ?;

-- name: SetAgentAccountDefault :exec
INSERT INTO agent_account_defaults (provider, account_id) VALUES (?, ?)
ON CONFLICT (provider) DO UPDATE SET account_id = excluded.account_id;

-- name: BindSessionAgentAccount :exec
INSERT INTO session_agent_accounts (session_id, provider, account_id, bound_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (session_id, provider, bound_at) DO UPDATE SET account_id = excluded.account_id;

-- name: GetSessionAgentAccount :one
SELECT account_id FROM session_agent_accounts
WHERE session_id = ? AND provider = ?
ORDER BY bound_at DESC
LIMIT 1;

-- Only a known source-time binding can prove that a turn used another account.
-- name: SessionAgentAccountChangedSince :one
SELECT EXISTS (
    SELECT 1 FROM sessions s
    JOIN session_agent_accounts source ON source.session_id = s.id
      AND source.provider = CASE s.harness WHEN 'claude-code' THEN 'claude' WHEN 'codex' THEN 'codex' ELSE '' END
    WHERE s.id = sqlc.arg(session_id)
      AND source.bound_at = (
          SELECT MAX(a.bound_at) FROM session_agent_accounts a
          WHERE a.session_id = s.id AND a.provider = source.provider
            AND a.bound_at <= sqlc.arg(requested_at)
      )
      AND source.account_id <> (
          SELECT a.account_id FROM session_agent_accounts a
          WHERE a.session_id = s.id AND a.provider = source.provider
          ORDER BY a.bound_at DESC LIMIT 1
      )
);

-- Account changes invalidate only this session's active provider's pending work.
-- name: CancelSessionTurnAutoResume :exec
UPDATE conversation_turns
SET usage_limit_auto_resume = 0, usage_limit_resume_error = ''
WHERE handled_by_session_id = sqlc.arg(session_id) AND usage_limit_auto_resume = 1
  AND EXISTS (
      SELECT 1 FROM sessions s WHERE s.id = conversation_turns.handled_by_session_id
        AND CASE s.harness WHEN 'claude-code' THEN 'claude' WHEN 'codex' THEN 'codex' ELSE '' END = CAST(sqlc.arg(provider) AS TEXT)
  );

-- Each usage event counts for the account its session used when the event
-- happened; an event older than the session's first binding counts for that
-- first account. Days are UTC calendar days.
-- name: AgentAccountUsageSince :many
SELECT
    CAST(COALESCE(
        (SELECT a.account_id FROM session_agent_accounts a
         WHERE a.session_id = b.session_id
           AND a.provider = CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
           AND a.bound_at <= e.created_at
         ORDER BY a.bound_at DESC LIMIT 1),
        (SELECT a.account_id FROM session_agent_accounts a
         WHERE a.session_id = b.session_id
           AND a.provider = CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
         ORDER BY a.bound_at ASC LIMIT 1),
        'device-' || CASE b.harness WHEN 'claude-code' THEN 'claude' ELSE 'codex' END
    ) AS TEXT) AS account_id,
    CAST(substr(e.created_at, 1, 10) AS TEXT) AS day,
    e.model_id,
    CAST(COALESCE(SUM(e.input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(e.cached_input_tokens), 0) AS INTEGER) AS cached_input_tokens,
    CAST(COALESCE(SUM(e.output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(e.estimated_cost_nanos), 0) AS INTEGER) AS estimated_cost_nanos
FROM model_usage_events e
JOIN usage_bindings b ON b.id = e.binding_id
WHERE e.created_at >= sqlc.arg(since)
  AND b.harness IN ('claude-code', 'codex')
GROUP BY 1, 2, 3
ORDER BY 2, 1, 3;
