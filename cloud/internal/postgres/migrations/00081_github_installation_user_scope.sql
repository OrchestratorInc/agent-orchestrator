-- +goose Up

-- How much of a GitHub App installation an organization's connection covers.
-- 'installation' (the default, and every existing row): the installation's
-- whole repository selection, as connected by someone who administers the
-- installation (the account itself, or an owner of the GitHub organization).
-- 'user': only the repositories scope_user_id can access on GitHub within the
-- installation. A GitHub organization member who is not an owner can connect
-- an installation this way into an organization where they are the only
-- member; every sync then lists repositories with that user's GitHub
-- authorization, and checkout and push grants re-check that user's access
-- before a token is minted.
ALTER TABLE ao_github_installations
    ADD COLUMN IF NOT EXISTS access_scope TEXT NOT NULL DEFAULT 'installation',
    ADD COLUMN IF NOT EXISTS scope_user_id UUID;

ALTER TABLE ao_github_installations
    ADD CONSTRAINT ao_github_installations_access_scope_check
    CHECK (access_scope IN ('installation', 'user')),
    ADD CONSTRAINT ao_github_installations_user_scope_user_check
    CHECK (access_scope = 'installation' OR scope_user_id IS NOT NULL),
    ADD CONSTRAINT ao_github_installations_scope_membership_fk
    FOREIGN KEY (org_id, scope_user_id)
    REFERENCES ao_org_memberships(org_id, user_id);

-- A user-scoped connection stands on one user's GitHub access, so it is only
-- honoured while that user is the organization's only active member: anyone
-- who joins later would otherwise reach repositories through someone else's
-- GitHub permissions. Every read of a user-scoped grant (listing, project
-- creation, checkout, pull request status) and every token minted through one
-- checks this. It answers false unless the transaction is acting for p_org_id
-- (ao.org_id), because only then can row-level security show all of the
-- organization's memberships; it fails closed rather than miscounting.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ao_github_user_scope_intact(p_org_id UUID, p_user_id UUID)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
AS $$
    SELECT p_org_id IS NOT NULL
       AND p_user_id IS NOT NULL
       AND coalesce(p_org_id = ao_current_org_id(), false)
       AND EXISTS (
           SELECT 1 FROM ao_org_memberships
           WHERE org_id = p_org_id AND user_id = p_user_id AND status = 'active'
       )
       AND NOT EXISTS (
           SELECT 1 FROM ao_org_memberships
           WHERE org_id = p_org_id AND user_id <> p_user_id AND status = 'active'
       )
$$;
-- +goose StatementEnd

-- +goose Down

-- Code from before user-scoped connections syncs every connection with the
-- installation's whole repository list, so a user-scoped row left behind would
-- be widened to the whole installation on its next sync. Revoke and disconnect
-- them first; the member reconnects once the newer code is back. The migration
-- role is subject to forced RLS, so lift FORCE for these statements only.
ALTER TABLE ao_github_installations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ao_github_repository_grants NO FORCE ROW LEVEL SECURITY;
UPDATE ao_github_repository_grants grant_row
SET revoked_at = now(), revoke_reason = 'github_user_scope_rolled_back'
FROM ao_github_installations installation
WHERE installation.org_id = grant_row.org_id
  AND installation.id = grant_row.installation_id
  AND installation.access_scope = 'user'
  AND grant_row.revoked_at IS NULL;
UPDATE ao_github_installations
SET status = 'disconnected', disconnected_at = now(), updated_at = now()
WHERE access_scope = 'user' AND status <> 'disconnected';
ALTER TABLE ao_github_repository_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE ao_github_installations FORCE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS ao_github_user_scope_intact(UUID, UUID);
ALTER TABLE ao_github_installations
    DROP CONSTRAINT IF EXISTS ao_github_installations_scope_membership_fk,
    DROP CONSTRAINT IF EXISTS ao_github_installations_user_scope_user_check,
    DROP CONSTRAINT IF EXISTS ao_github_installations_access_scope_check,
    DROP COLUMN IF EXISTS scope_user_id,
    DROP COLUMN IF EXISTS access_scope;
