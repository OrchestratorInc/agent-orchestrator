-- +goose Up

-- ao_artifact_shares lists the public links users created for single files
-- from local sessions. One row is one active link; revoking deletes the row.
-- The bytes live in the artifact bucket at s3_key and version_id names the
-- object version the link serves.
CREATE TABLE ao_artifact_shares (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    slug TEXT NOT NULL UNIQUE
        CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    name TEXT NOT NULL CHECK (name <> '' AND octet_length(name) <= 255),
    content_type TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    s3_key TEXT NOT NULL,
    version_id TEXT NOT NULL CHECK (version_id <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ao_artifact_shares_user_idx ON ao_artifact_shares(user_id);

ALTER TABLE ao_artifact_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_artifact_shares FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_artifact_shares_owner_policy ON ao_artifact_shares
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());
-- Serving a link looks the row up by slug with no signed-in user.
CREATE POLICY ao_artifact_shares_service_policy ON ao_artifact_shares
    USING (ao_service_context())
    WITH CHECK (ao_service_context());

-- +goose Down

DROP TABLE IF EXISTS ao_artifact_shares;
