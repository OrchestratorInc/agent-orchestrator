-- +goose Up

-- A prepared VM snapshot per project and harness: the harness snapshot with
-- the project's repository already cloned, so a new session starts from it and
-- only fetches what changed. Built by a throwaway VM, never from a live
-- session, so it carries no session credentials or conversation.
CREATE TABLE ao_project_sandbox_snapshots (
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    provider TEXT NOT NULL CHECK (btrim(provider) <> ''),
    harness TEXT NOT NULL CHECK (btrim(harness) <> ''),
    snapshot_id TEXT NOT NULL CHECK (btrim(snapshot_id) <> ''),
    base_snapshot_id TEXT NOT NULL CHECK (btrim(base_snapshot_id) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, project_id, provider, harness),
    FOREIGN KEY (org_id, project_id) REFERENCES ao_projects(org_id, id) ON DELETE CASCADE
);
ALTER TABLE ao_project_sandbox_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_project_sandbox_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_project_sandbox_snapshots_tenant_policy ON ao_project_sandbox_snapshots
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());

-- +goose Down
DROP TABLE ao_project_sandbox_snapshots;
