-- +goose Up

-- Prepared VM snapshots are keyed by repository rather than project, so a
-- project deleted and added again, or a second project on the same repository,
-- boots from the snapshot already built instead of cloning again. Deleting a
-- project no longer deletes the row: a snapshot unused for a week is collected
-- instead (last_used_at), which also covers snapshots whose project is gone.
--
-- repository_key is "github:<repository id>" when the project knows its GitHub
-- repository id, otherwise "url:<owner/name>". repository_identity is the
-- owner/name the snapshot was cloned from: the worker requires the checkout's
-- origin to match the session's repository, so a renamed or transferred
-- repository must not reuse a snapshot cloned under its old name.
CREATE TABLE ao_repository_sandbox_snapshots (
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    repository_key TEXT NOT NULL CHECK (btrim(repository_key) <> ''),
    provider TEXT NOT NULL CHECK (btrim(provider) <> ''),
    harness TEXT NOT NULL CHECK (btrim(harness) <> ''),
    repository_identity TEXT NOT NULL CHECK (btrim(repository_identity) <> ''),
    snapshot_id TEXT NOT NULL CHECK (btrim(snapshot_id) <> ''),
    base_snapshot_id TEXT NOT NULL CHECK (btrim(base_snapshot_id) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, repository_key, provider, harness)
);
CREATE INDEX ao_repository_sandbox_snapshots_idle_idx
    ON ao_repository_sandbox_snapshots (provider, last_used_at);
ALTER TABLE ao_repository_sandbox_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_repository_sandbox_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_repository_sandbox_snapshots_tenant_policy ON ao_repository_sandbox_snapshots
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());
-- The idle collector scans across organizations.
CREATE POLICY ao_repository_sandbox_snapshots_service_policy ON ao_repository_sandbox_snapshots
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- Carry existing project snapshots over so they keep serving sessions and stay
-- visible to the collector. The identity is the project's current GitHub
-- owner/name, which is what each snapshot was cloned from unless the
-- repository was renamed since; that case fails the identity check and simply
-- rebuilds. Where two projects share a repository the newest snapshot wins.
-- The migration role is subject to forced RLS like every other role, so the
-- copy lifts FORCE for its own statements and restores it before commit.
ALTER TABLE ao_projects NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ao_project_sandbox_snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ao_repository_sandbox_snapshots NO FORCE ROW LEVEL SECURITY;
INSERT INTO ao_repository_sandbox_snapshots (
    org_id, repository_key, provider, harness, repository_identity,
    snapshot_id, base_snapshot_id, created_at, last_used_at
)
SELECT DISTINCT ON (org_id, repository_key, provider, harness)
    org_id, repository_key, provider, harness, repository_identity,
    snapshot_id, base_snapshot_id, created_at, created_at
FROM (
    SELECT snapshot.org_id, snapshot.provider, snapshot.harness,
        snapshot.snapshot_id, snapshot.base_snapshot_id, snapshot.created_at,
        identity.value AS repository_identity,
        CASE WHEN project.github_repository_id IS NOT NULL
            THEN 'github:' || project.github_repository_id
            ELSE 'url:' || identity.value
        END AS repository_key
    FROM ao_project_sandbox_snapshots snapshot
    JOIN ao_projects project
      ON project.org_id = snapshot.org_id AND project.id = snapshot.project_id
    CROSS JOIN LATERAL (
        SELECT lower(regexp_replace(substring(project.repository_url
            FROM '^https://github\.com/([^/?#]+/[^/?#]+)/?$'), '\.git$', '')) AS value
    ) identity
    WHERE identity.value IS NOT NULL
) carried
ORDER BY org_id, repository_key, provider, harness, created_at DESC;
ALTER TABLE ao_repository_sandbox_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE ao_projects FORCE ROW LEVEL SECURITY;
DROP TABLE ao_project_sandbox_snapshots;

-- +goose Down
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
DROP TABLE ao_repository_sandbox_snapshots;
