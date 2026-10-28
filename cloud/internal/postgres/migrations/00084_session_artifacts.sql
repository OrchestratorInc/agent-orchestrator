-- +goose Up

-- Every statement here can run again on a database that already applied it,
-- so the migration can be renumbered after reaching an environment.

-- Fail fast instead of queueing behind a long transaction on ao_sessions or
-- ao_pull_requests; goose runs the migration in one transaction.
SET LOCAL lock_timeout = '5s';

-- requested_output is the deliverable a session was created for ('' for the
-- ordinary task flow, 'artifact' for a file deliverable). output_type is what
-- the session currently owns: no output, a pull request, artifact files, or
-- both. Both columns have defaults.
ALTER TABLE ao_sessions
    ADD COLUMN IF NOT EXISTS requested_output TEXT NOT NULL DEFAULT ''
        CONSTRAINT ao_sessions_requested_output_check
        CHECK (requested_output IN ('', 'artifact')),
    ADD COLUMN IF NOT EXISTS output_type TEXT NOT NULL DEFAULT 'none'
        CONSTRAINT ao_sessions_output_type_check
        CHECK (output_type IN ('none', 'pr', 'artifact', 'pr_artifact'));

-- Session reads count a session's pull requests into its output type.
CREATE INDEX IF NOT EXISTS ao_pull_requests_session_idx
    ON ao_pull_requests(org_id, session_id);

-- ao_session_artifacts lists one session's committed artifact files. The bytes
-- live in the tenant artifact bucket at s3_key; version_id names the exact
-- object version a read serves.
CREATE TABLE IF NOT EXISTS ao_session_artifacts (
    org_id UUID NOT NULL,
    session_id UUID NOT NULL,
    path TEXT NOT NULL CHECK (path <> '' AND octet_length(path) <= 1024),
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('html', 'markdown', 'file')),
    content_type TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    s3_key TEXT NOT NULL,
    version_id TEXT NOT NULL CHECK (version_id <> ''),
    mtime TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, path),
    CONSTRAINT ao_session_artifacts_session_fk
        FOREIGN KEY (org_id, session_id)
        REFERENCES ao_sessions(org_id, id)
        ON DELETE CASCADE
);

ALTER TABLE ao_session_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_session_artifacts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ao_session_artifacts_tenant_policy ON ao_session_artifacts;
CREATE POLICY ao_session_artifacts_tenant_policy ON ao_session_artifacts
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());
DROP POLICY IF EXISTS ao_session_artifacts_service_policy ON ao_session_artifacts;
CREATE POLICY ao_session_artifacts_service_policy ON ao_session_artifacts
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- ao_artifact_object_versions lists every artifact object version in the
-- bucket that no ao_session_artifacts row serves: uploads a worker has not
-- committed yet ('upload') and versions a commit replaced or deleted
-- ('replaced'). The sweeper deletes each version from the bucket once
-- collect_after passes; collecting_until is its claim on the row.
CREATE TABLE IF NOT EXISTS ao_artifact_object_versions (
    org_id UUID NOT NULL,
    session_id UUID NOT NULL,
    s3_key TEXT NOT NULL,
    version_id TEXT NOT NULL CHECK (version_id <> ''),
    reason TEXT NOT NULL CHECK (reason IN ('upload', 'replaced')),
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    collect_after TIMESTAMPTZ NOT NULL,
    collecting_until TIMESTAMPTZ,
    PRIMARY KEY (s3_key, version_id),
    CONSTRAINT ao_artifact_object_versions_session_fk
        FOREIGN KEY (org_id, session_id)
        REFERENCES ao_sessions(org_id, id)
        ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ao_artifact_object_versions_session_idx
    ON ao_artifact_object_versions(session_id, reason);
CREATE INDEX IF NOT EXISTS ao_artifact_object_versions_collect_idx
    ON ao_artifact_object_versions(collect_after);

ALTER TABLE ao_artifact_object_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_artifact_object_versions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ao_artifact_object_versions_tenant_policy ON ao_artifact_object_versions;
CREATE POLICY ao_artifact_object_versions_tenant_policy ON ao_artifact_object_versions
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());
DROP POLICY IF EXISTS ao_artifact_object_versions_service_policy ON ao_artifact_object_versions;
CREATE POLICY ao_artifact_object_versions_service_policy ON ao_artifact_object_versions
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- +goose Down

-- Dropping these tables leaves their objects in the bucket; empty the
-- session prefixes first if the data must go.
DROP TABLE IF EXISTS ao_artifact_object_versions;
DROP INDEX IF EXISTS ao_pull_requests_session_idx;
DROP TABLE IF EXISTS ao_session_artifacts;
ALTER TABLE ao_sessions
    DROP COLUMN IF EXISTS output_type,
    DROP COLUMN IF EXISTS requested_output;
