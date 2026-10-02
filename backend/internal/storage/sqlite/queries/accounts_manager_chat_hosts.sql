-- name: InsertAccountsManagerChatHost :exec
INSERT INTO accounts_manager_chat_hosts(session_id, generation, host_identity)
VALUES (?, ?, ?) ON CONFLICT(session_id, generation) DO NOTHING;

-- name: GetAccountsManagerChatHost :one
SELECT host_identity FROM accounts_manager_chat_hosts WHERE session_id = ? AND generation = ?;
