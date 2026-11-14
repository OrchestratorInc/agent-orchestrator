-- +goose Up
-- Storage management: the user's cleanup preferences (one row, seeded so a read
-- never handles absence), per-session dependency offload facts, and an audit
-- log of every storage action. Times are Unix milliseconds; 0 means never.
-- Idempotent: a pre-release build applied this file under an earlier version
-- number, so a database may already hold these tables.
CREATE TABLE IF NOT EXISTS storage_settings (
    id                          INTEGER PRIMARY KEY CHECK (id = 1),
    auto_clean                  INTEGER NOT NULL DEFAULT 1 CHECK (auto_clean IN (0, 1)),
    offload_enabled             INTEGER NOT NULL DEFAULT 1 CHECK (offload_enabled IN (0, 1)),
    offload_idle_hours          INTEGER NOT NULL DEFAULT 48 CHECK (offload_idle_hours BETWEEN 1 AND 8760),
    offload_no_pr_grace_hours   INTEGER NOT NULL DEFAULT 2 CHECK (offload_no_pr_grace_hours BETWEEN 1 AND 8760),
    offload_merged_idle_minutes INTEGER NOT NULL DEFAULT 30 CHECK (offload_merged_idle_minutes BETWEEN 0 AND 10080),
    ended_worktree_days         INTEGER NOT NULL DEFAULT 7 CHECK (ended_worktree_days BETWEEN 0 AND 3650),
    backup_days                 INTEGER NOT NULL DEFAULT 14 CHECK (backup_days BETWEEN 1 AND 3650),
    log_days                    INTEGER NOT NULL DEFAULT 14 CHECK (log_days BETWEEN 1 AND 3650),
    clean_dev_profiles          INTEGER NOT NULL DEFAULT 0 CHECK (clean_dev_profiles IN (0, 1)),
    dev_profile_days            INTEGER NOT NULL DEFAULT 7 CHECK (dev_profile_days BETWEEN 1 AND 3650),
    template_days               INTEGER NOT NULL DEFAULT 30 CHECK (template_days BETWEEN 1 AND 3650),
    updated_at_ms               INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO storage_settings (id) VALUES (1);

-- One row per session whose dependencies were ever offloaded. Facts only:
-- whether a session reads as offloaded is derived from the two times.
CREATE TABLE IF NOT EXISTS session_storage_facts (
    session_id      TEXT NOT NULL PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    offloaded_at_ms INTEGER NOT NULL DEFAULT 0,
    offloaded_bytes INTEGER NOT NULL DEFAULT 0,
    offloaded_paths TEXT NOT NULL DEFAULT '[]',
    template_keys   TEXT NOT NULL DEFAULT '{}',
    restored_at_ms  INTEGER NOT NULL DEFAULT 0,
    restore_error   TEXT NOT NULL DEFAULT ''
);

-- What every cleanup did or would do. session_id is plain text, not a foreign
-- key: the log outlives the sessions it mentions.
CREATE TABLE IF NOT EXISTS storage_actions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at_ms      INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    bytes      INTEGER NOT NULL DEFAULT 0,
    dry_run    INTEGER NOT NULL DEFAULT 0 CHECK (dry_run IN (0, 1)),
    result     TEXT NOT NULL CHECK (result IN ('planned', 'done', 'skipped', 'failed')),
    error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_storage_actions_at ON storage_actions (at_ms);

-- +goose Down
DROP INDEX IF EXISTS idx_storage_actions_at;
DROP TABLE IF EXISTS storage_actions;
DROP TABLE IF EXISTS session_storage_facts;
DROP TABLE IF EXISTS storage_settings;
