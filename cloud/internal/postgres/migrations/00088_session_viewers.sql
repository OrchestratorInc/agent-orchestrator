-- +goose Up

-- Who has a session open right now, for a shared session's viewer list. One
-- row per open live event stream (a person may have several), written by the
-- control plane when the stream opens, refreshed while it stays open, and
-- removed when it closes. A replica that dies leaves rows behind; readers
-- ignore rows not refreshed within a minute, and a later join prunes them.
CREATE TABLE ao_session_viewers (
    connection_id UUID PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES ao_sessions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    -- The access the viewer had when the stream opened: an organization
    -- member, or a share grant's editor or viewer role.
    role TEXT NOT NULL CHECK (role IN ('member', 'editor', 'viewer')),
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ao_session_viewers_session_idx ON ao_session_viewers (session_id, seen_at);

ALTER TABLE ao_session_viewers ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_session_viewers FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_session_viewers_tenant_policy ON ao_session_viewers
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());

-- Tell a session's live streams that its viewer list changed. A refresh only
-- moves seen_at and changes nothing a viewer sees, so it does not notify.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_session_live() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('ao_session_live', OLD.session_id::text);
    ELSE
        PERFORM pg_notify('ao_session_live', NEW.session_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_session_viewers_live_notify
    AFTER INSERT OR DELETE
    ON ao_session_viewers
    FOR EACH ROW EXECUTE FUNCTION ao_notify_session_live();

-- +goose Down
DROP TRIGGER IF EXISTS ao_session_viewers_live_notify ON ao_session_viewers;
DROP FUNCTION IF EXISTS ao_notify_session_live();
DROP TABLE IF EXISTS ao_session_viewers;
