-- +goose Up

-- ao_artifact_shares lists the public links users created for single files
-- from local sessions. Every row uses one of the user's link slots, revoked or
-- not: revoking sets revoked_at, which stops the link serving, and keeps the
-- row so the slot stays used.
-- The bytes live in the artifact bucket at s3_key and version_id names the
-- object version the link serves. document_key is a hash of the published
-- document's exact project, feature and file name, so publishing the same
-- document again replaces the version its one link serves.
CREATE TABLE ao_artifact_shares (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    slug TEXT NOT NULL UNIQUE
        CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    name TEXT NOT NULL CHECK (name <> '' AND octet_length(name) <= 255),
    document_key TEXT NOT NULL CHECK (document_key <> ''),
    content_type TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    s3_key TEXT NOT NULL,
    version_id TEXT NOT NULL CHECK (version_id <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX ao_artifact_shares_document_key ON ao_artifact_shares(user_id, document_key);

ALTER TABLE ao_artifact_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_artifact_shares FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_artifact_shares_owner_policy ON ao_artifact_shares
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());
-- Serving a link looks the row up by slug with no signed-in user.
CREATE POLICY ao_artifact_shares_service_policy ON ao_artifact_shares
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- Share object versions use the same sweeper queue as session artifacts. A
-- share version belongs to a user rather than a session, so its row carries
-- user_id and no organization or session. user_id has no foreign key: the
-- queued version must still be deleted after its user is.
ALTER TABLE ao_artifact_object_versions
    ALTER COLUMN org_id DROP NOT NULL,
    ALTER COLUMN session_id DROP NOT NULL,
    ADD COLUMN user_id UUID,
    ADD CONSTRAINT ao_artifact_object_versions_owner_check CHECK (
        (org_id IS NOT NULL AND session_id IS NOT NULL AND user_id IS NULL)
        OR (org_id IS NULL AND session_id IS NULL AND user_id IS NOT NULL
            AND s3_key LIKE 'shares/%')
    );
CREATE INDEX ao_artifact_object_versions_user_idx
    ON ao_artifact_object_versions(user_id) WHERE user_id IS NOT NULL;
CREATE POLICY ao_artifact_object_versions_share_owner_policy ON ao_artifact_object_versions
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());

-- +goose Down

-- Dropping these leaves share objects in the bucket; empty the shares/
-- prefix first if the data must go.
DROP POLICY IF EXISTS ao_artifact_object_versions_share_owner_policy ON ao_artifact_object_versions;
DELETE FROM ao_artifact_object_versions WHERE user_id IS NOT NULL;
DROP INDEX IF EXISTS ao_artifact_object_versions_user_idx;
ALTER TABLE ao_artifact_object_versions
    DROP CONSTRAINT IF EXISTS ao_artifact_object_versions_owner_check,
    DROP COLUMN IF EXISTS user_id,
    ALTER COLUMN org_id SET NOT NULL,
    ALTER COLUMN session_id SET NOT NULL;
DROP TABLE IF EXISTS ao_artifact_shares;
