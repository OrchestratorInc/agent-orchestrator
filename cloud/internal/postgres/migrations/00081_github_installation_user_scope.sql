-- +goose Up

-- How much of a GitHub App installation an organization's connection covers.
-- 'installation' (the default, and every existing row): the installation's
-- whole repository selection, as connected by someone who administers the
-- installation (the account itself, or an owner of the GitHub organization).
-- 'user': only the repositories scope_user_id can access on GitHub within the
-- installation. A GitHub organization member who is not an owner can connect
-- an installation this way into their personal AO organization; every sync
-- then lists repositories with that user's GitHub authorization, and checkout
-- and push grants re-check that user's access before a token is minted.
ALTER TABLE ao_github_installations
    ADD COLUMN IF NOT EXISTS access_scope TEXT NOT NULL DEFAULT 'installation',
    ADD COLUMN IF NOT EXISTS scope_user_id UUID;

ALTER TABLE ao_github_installations
    ADD CONSTRAINT ao_github_installations_access_scope_check
    CHECK (access_scope IN ('installation', 'user')),
    ADD CONSTRAINT ao_github_installations_user_scope_user_check
    CHECK (access_scope = 'installation' OR scope_user_id IS NOT NULL);

-- +goose Down
ALTER TABLE ao_github_installations
    DROP CONSTRAINT IF EXISTS ao_github_installations_user_scope_user_check,
    DROP CONSTRAINT IF EXISTS ao_github_installations_access_scope_check,
    DROP COLUMN IF EXISTS scope_user_id,
    DROP COLUMN IF EXISTS access_scope;
