-- name: UpsertAccountsManagerRoutingPolicy :exec
INSERT INTO accounts_manager_routing_policies (provider, enabled, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(provider) DO UPDATE SET
    enabled = excluded.enabled,
    updated_at = excluded.updated_at;

-- name: DeleteAccountsManagerRoutingPolicyAccounts :exec
DELETE FROM accounts_manager_routing_policy_accounts WHERE provider = ?;

-- name: InsertAccountsManagerRoutingPolicyAccount :exec
INSERT INTO accounts_manager_routing_policy_accounts (provider, account_id, position)
VALUES (?, ?, ?);

-- name: GetAccountsManagerRoutingPolicy :one
SELECT provider, enabled, updated_at
FROM accounts_manager_routing_policies
WHERE provider = ?;

-- name: ListAccountsManagerRoutingPolicyAccounts :many
SELECT account_id
FROM accounts_manager_routing_policy_accounts
WHERE provider = ?
ORDER BY position ASC;

-- name: InsertAccountsManagerSessionRoute :execrows
INSERT INTO accounts_manager_session_bindings (session_id, provider, connection_mode, account_id, revision, created_at, updated_at)
VALUES (?, ?, ?, ?, (SELECT accounts_manager_binding_clock.revision + 1 FROM accounts_manager_binding_clock WHERE id = 1), ?, ?)
ON CONFLICT(session_id, provider) DO NOTHING;

-- name: GetAccountsManagerSessionRoute :one
SELECT session_id, provider, connection_mode, account_id, revision, created_at, updated_at
FROM accounts_manager_session_bindings
WHERE session_id = ? AND provider = ?;

-- name: CompareAndSwapAccountsManagerSessionRoute :execrows
UPDATE accounts_manager_session_bindings
SET connection_mode = ?, account_id = ?, revision = (SELECT accounts_manager_binding_clock.revision + 1 FROM accounts_manager_binding_clock WHERE id = 1), updated_at = ?
WHERE session_id = ? AND provider = ? AND accounts_manager_session_bindings.revision = ?;

-- name: ListAccountsManagerSessionBindings :many
SELECT b.session_id, b.provider, b.connection_mode, b.account_id, b.revision, b.created_at, b.updated_at,
       EXISTS (SELECT 1 FROM accounts_manager_switches AS op WHERE op.session_id = b.session_id
               AND op.phase IN ('stopping', 'stopped', 'recovery_required')
               UNION ALL SELECT 1 FROM accounts_manager_removals AS removal WHERE removal.account_id = b.account_id AND removal.phase != 'cancelled') AS blocked
FROM accounts_manager_session_bindings AS b
ORDER BY b.session_id, b.provider;

-- name: GetAccountsManagerBindingRevision :one
SELECT revision FROM accounts_manager_binding_clock WHERE id = 1;
