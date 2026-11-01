-- +goose Up
-- IDs are random and local to one install and project. No repo data is derived.
CREATE TABLE usage_project_identity (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    install_id TEXT NOT NULL,
    telemetry_id TEXT NOT NULL UNIQUE,
    PRIMARY KEY (project_id, install_id)
);

-- One row per session bounds the pending Chat count independently of traffic.
CREATE TABLE usage_message_summary (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    pending_count INTEGER NOT NULL DEFAULT 0,
    first_at TIMESTAMP,
    last_at TIMESTAMP
);

-- The key is a local provider identity. It never leaves the daemon.
CREATE TABLE usage_pr_measurement (
    pr_key TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    historical INTEGER NOT NULL DEFAULT 0,
    historical_merged INTEGER NOT NULL DEFAULT 0,
    associated_reported INTEGER NOT NULL DEFAULT 0,
    merged_reported INTEGER NOT NULL DEFAULT 0
);

-- Existing worker associations, including merged PRs, are intentionally
-- eligible for one historical measurement on first enabled startup.
INSERT OR IGNORE INTO usage_pr_measurement (pr_key, project_id, historical, historical_merged)
SELECT CASE WHEN pr.provider_id <> '' AND pr.provider <> ''
    THEN pr.provider || ':' || pr.host || ':' || pr.provider_id
    ELSE pr.url END, sessions.project_id, 1, pr.pr_state = 'merged'
FROM pr JOIN sessions ON sessions.id = pr.session_id
WHERE sessions.kind = 'worker';

-- +goose Down
DROP TABLE usage_pr_measurement;
DROP TABLE usage_message_summary;
DROP TABLE usage_project_identity;
