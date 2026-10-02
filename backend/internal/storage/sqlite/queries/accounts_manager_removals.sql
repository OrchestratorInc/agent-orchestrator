-- name: InsertAccountsManagerRemoval :exec
INSERT INTO accounts_manager_removals (id, account_id, impact, phase, created_at, updated_at)
VALUES (?, ?, ?, 'requested', ?, ?);

-- name: GetAccountsManagerRemoval :one
SELECT * FROM accounts_manager_removals WHERE id = ?;

-- name: GetAccountsManagerAccountRemoval :one
SELECT * FROM accounts_manager_removals WHERE account_id = ? AND phase != 'cancelled';

-- name: ListActiveAccountsManagerRemovals :many
SELECT * FROM accounts_manager_removals WHERE phase NOT IN ('complete', 'cancelled') ORDER BY rowid;

-- name: UpdateAccountsManagerRemoval :execrows
UPDATE accounts_manager_removals SET impact = ?, phase = ?, error_code = ?, stop_started = ?, bindings_revoked = ?, updated_at = ?
WHERE id = ? AND phase NOT IN ('complete', 'cancelled');

-- name: AccountsManagerAccountDeleting :one
SELECT EXISTS (SELECT 1 FROM accounts_manager_removals WHERE account_id = ? AND phase != 'cancelled') AS deleting;

-- name: RememberAccountsManagerRemovedChoices :exec
INSERT INTO accounts_manager_removed_choices(session_id, provider, account_id, revision, created_at, updated_at)
SELECT b.session_id, b.provider, b.account_id, (SELECT revision + 1 FROM accounts_manager_binding_clock WHERE id = 1), b.created_at, sqlc.arg(updated_at)
FROM accounts_manager_session_bindings AS b WHERE b.account_id = sqlc.arg(account_id);

-- name: DeleteAccountsManagerRemovedBindings :exec
DELETE FROM accounts_manager_session_bindings WHERE account_id = ?;

-- name: GetAccountsManagerRemovedChoice :one
SELECT * FROM accounts_manager_removed_choices WHERE session_id = ? AND provider = ?;

-- name: ClearAccountsManagerRemovedChoice :execrows
DELETE FROM accounts_manager_removed_choices WHERE session_id = ? AND provider = ? AND revision = ?;
