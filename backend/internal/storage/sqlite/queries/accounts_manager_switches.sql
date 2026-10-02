-- name: InsertAccountsManagerSwitch :exec
INSERT INTO accounts_manager_switches (
    id, session_id, provider, source_mode, source_account_id, source_revision,
    source_owner, source_runtime_handle_id, target_mode, target_account_id,
    target_generation, policy, new_conversation, phase, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'requested', ?, ?);

-- name: GetAccountsManagerSwitch :one
SELECT * FROM accounts_manager_switches WHERE id = ?;

-- name: GetLatestAccountsManagerSwitch :one
SELECT * FROM accounts_manager_switches WHERE session_id = ? ORDER BY rowid DESC LIMIT 1;

-- name: ListActiveAccountsManagerSwitches :many
SELECT * FROM accounts_manager_switches WHERE phase NOT IN ('ready', 'cancelled', 'failed') ORDER BY rowid;

-- name: AdvanceAccountsManagerSwitch :execrows
UPDATE accounts_manager_switches
SET phase = sqlc.arg(next_phase), error_code = sqlc.arg(error_code), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND phase = sqlc.arg(expected_phase);

-- name: CommitAccountsManagerSwitch :execrows
UPDATE accounts_manager_switches SET phase = 'committed', target_revision = ?, error_code = '', updated_at = ?
WHERE id = ? AND phase = 'stopped';

-- name: RetryAccountsManagerSwitch :execrows
UPDATE accounts_manager_switches
SET phase = 'committed', target_revision = ?, target_generation = ?, retired_target_generation = ?, retired_target_handle_id = ?, error_code = '', updated_at = ?
WHERE id = ? AND phase = 'recovery_required';

-- name: PrepareAccountsManagerSwitchStop :execrows
UPDATE accounts_manager_switches
SET phase = 'stopping', empty_source = ?, source_native_conversation_id = ?, error_code = '', updated_at = ?
WHERE id = ? AND phase IN ('waiting', 'recovery_required') AND target_revision = 0;
