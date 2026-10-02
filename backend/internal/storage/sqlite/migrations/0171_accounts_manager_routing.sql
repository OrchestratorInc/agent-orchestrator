-- +goose Up
CREATE TABLE accounts_manager_routing_policies (
    provider TEXT PRIMARY KEY CHECK (provider IN ('codex', 'claude')),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    updated_at DATETIME NOT NULL
);

CREATE TABLE accounts_manager_routing_policy_accounts (
    provider TEXT NOT NULL REFERENCES accounts_manager_routing_policies(provider) ON DELETE CASCADE,
    account_id TEXT NOT NULL CHECK (length(account_id) > 0),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (provider, account_id),
    UNIQUE (provider, position)
);

CREATE TABLE accounts_manager_session_routes (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude')),
    account_id TEXT NOT NULL CHECK (length(account_id) > 0),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (session_id, provider)
);

-- +goose Down
DROP TABLE accounts_manager_session_routes;
DROP TABLE accounts_manager_routing_policy_accounts;
DROP TABLE accounts_manager_routing_policies;
