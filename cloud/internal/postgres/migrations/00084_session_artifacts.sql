-- +goose Up

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

-- ao_session_artifacts lists one session's committed artifact files. The bytes
-- live in the tenant artifact bucket at s3_key; version_id names the exact
-- object version a read serves.
CREATE TABLE ao_session_artifacts (
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
CREATE INDEX ao_session_artifacts_org_session_idx
    ON ao_session_artifacts(org_id, session_id);

ALTER TABLE ao_session_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_session_artifacts FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_session_artifacts_tenant_policy ON ao_session_artifacts
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());
CREATE POLICY ao_session_artifacts_service_policy ON ao_session_artifacts
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- +goose Down

DROP TABLE IF EXISTS ao_session_artifacts;
ALTER TABLE ao_sessions
    DROP COLUMN IF EXISTS output_type,
    DROP COLUMN IF EXISTS requested_output;
