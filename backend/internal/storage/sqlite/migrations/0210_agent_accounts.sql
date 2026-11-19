-- +goose Up
-- Subscription accounts AO launches Claude Code and Codex with. Each account is
-- a config folder of the provider's own CLI (CLAUDE_CONFIG_DIR or CODEX_HOME);
-- the computer's own login is implicit and never stored. home is the exact path
-- string the CLI is launched with, because Claude keys its Keychain entry on it.
CREATE TABLE agent_accounts (
    id         TEXT PRIMARY KEY CHECK (trim(id) <> ''),
    provider   TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
    home       TEXT NOT NULL UNIQUE CHECK (trim(home) <> ''),
    email      TEXT NOT NULL DEFAULT '',
    plan       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

-- The account new sessions of a provider start on. No row means the computer's
-- own login.
CREATE TABLE agent_account_defaults (
    provider   TEXT PRIMARY KEY CHECK (provider IN ('claude', 'codex')),
    account_id TEXT NOT NULL CHECK (trim(account_id) <> '')
);

-- Which account a session uses for each provider, from bound_at on. Earlier
-- rows stay so usage is attributed to the account a session used at the time.
CREATE TABLE session_agent_accounts (
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    provider   TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
    account_id TEXT NOT NULL CHECK (trim(account_id) <> ''),
    bound_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, provider, bound_at)
);

-- Every session that exists already ran on the computer's own login. Recording
-- that keeps it there when a default account is picked later (a session with no
-- binding would otherwise read as the default) and attributes its usage to the
-- login it used.
INSERT INTO session_agent_accounts (session_id, provider, account_id, bound_at)
SELECT sessions.id, providers.provider, 'device-' || providers.provider, sessions.created_at
FROM sessions
CROSS JOIN (SELECT 'claude' AS provider UNION ALL SELECT 'codex') AS providers;

-- +goose Down
DROP TABLE session_agent_accounts;
DROP TABLE agent_account_defaults;
DROP TABLE agent_accounts;
