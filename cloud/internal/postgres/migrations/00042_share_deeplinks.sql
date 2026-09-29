-- Deep-link session shares (ao-app://share/...) are single-use invitations:
-- the first successful redeem atomically flips the link from 'active' to
-- 'redeemed' so the same secret can never mint a second grant. Existing
-- multi-use links keep single_use = false and are unaffected.
-- +goose Up
ALTER TABLE ao_project_share_links
    ADD COLUMN single_use BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN redeemed_at TIMESTAMPTZ,
    ADD COLUMN redeemed_by_user_id UUID REFERENCES ao_users(id) ON DELETE SET NULL;
ALTER TABLE ao_project_share_links
    DROP CONSTRAINT ao_project_share_links_status_check;
ALTER TABLE ao_project_share_links
    ADD CONSTRAINT ao_project_share_links_status_check
    CHECK (status IN ('active', 'revoked', 'redeemed'));
ALTER TABLE ao_project_share_links
    ADD CONSTRAINT ao_project_share_links_redeemed_check
    CHECK (status <> 'redeemed' OR (single_use AND redeemed_at IS NOT NULL));

-- +goose Down
UPDATE ao_project_share_links SET status = 'revoked' WHERE status = 'redeemed';
ALTER TABLE ao_project_share_links
    DROP CONSTRAINT ao_project_share_links_redeemed_check;
ALTER TABLE ao_project_share_links
    DROP CONSTRAINT ao_project_share_links_status_check;
ALTER TABLE ao_project_share_links
    ADD CONSTRAINT ao_project_share_links_status_check
    CHECK (status IN ('active', 'revoked'));
ALTER TABLE ao_project_share_links
    DROP COLUMN redeemed_by_user_id,
    DROP COLUMN redeemed_at,
    DROP COLUMN single_use;
