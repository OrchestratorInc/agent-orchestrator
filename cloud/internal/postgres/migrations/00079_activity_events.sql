-- +goose Up

-- A per-organization activity trail for support: who signed up, created what,
-- which sessions were requested, how long their sandboxes took to become ready,
-- and how they failed. Rows are written fire-and-forget by the control plane and
-- read by operators (cloud/docs/support-runbook.md). attrs holds small, already
-- redacted diagnostics only: never tokens, prompts or repository contents.
-- Rows older than 90 days are pruned by the control plane.
CREATE TABLE ao_activity_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    -- No foreign keys on the user or session: the trail must outlive a deleted
    -- session, and background events (the reconciler) have no user.
    user_id UUID,
    session_id UUID,
    kind TEXT NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_.]{0,63}$'),
    provider TEXT NOT NULL DEFAULT '' CHECK (length(provider) <= 32),
    attrs JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(attrs) = 'object' AND pg_column_size(attrs) <= 8192
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ao_activity_events_org_idx ON ao_activity_events (org_id, created_at DESC);
CREATE INDEX ao_activity_events_user_idx ON ao_activity_events (user_id, created_at DESC)
    WHERE user_id IS NOT NULL;
CREATE INDEX ao_activity_events_session_idx ON ao_activity_events (session_id, created_at DESC)
    WHERE session_id IS NOT NULL;
-- The retention prune scans by age across organizations.
CREATE INDEX ao_activity_events_created_idx ON ao_activity_events (created_at);

ALTER TABLE ao_activity_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_activity_events FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_activity_events_tenant_policy ON ao_activity_events
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
CREATE POLICY ao_activity_events_service_policy ON ao_activity_events
    USING (ao_service_context()) WITH CHECK (ao_service_context());

-- The operator detail behind a sandbox's startup error (#6447 stores only the
-- user-facing code and message): a bounded, redacted excerpt of the provider
-- failure and, for Coder, the tail of the workspace build and agent startup
-- logs. The API shows it only for an organization's own (BYO) compute.
ALTER TABLE ao_sandboxes
    ADD COLUMN startup_error_detail TEXT NOT NULL DEFAULT ''
        CHECK (length(startup_error_detail) <= 4096);

-- +goose Down
ALTER TABLE ao_sandboxes DROP COLUMN startup_error_detail;
DROP TABLE ao_activity_events;
