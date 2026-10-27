-- +goose Up

-- The composer lease is a shared session's hard lock on talking to its
-- agent: only the holder may send or steer. One row per session, created on
-- first use. epoch is a fencing token: it increases every time the lease
-- passes to a different person, and a send must present the epoch it was
-- granted, so someone whose lease expired or was taken over cannot send on a
-- stale grant. All times are the database's.
CREATE TABLE ao_composer_leases (
    session_id UUID PRIMARY KEY REFERENCES ao_sessions(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    -- NULL while nobody holds the lease.
    holder_user_id UUID REFERENCES ao_users(id) ON DELETE SET NULL,
    epoch BIGINT NOT NULL DEFAULT 0 CHECK (epoch >= 0),
    expires_at TIMESTAMPTZ,
    -- The holder's last keystroke; an idle holder loses the lease.
    last_input_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The expiry sweep scans held leases by expiry across organizations.
CREATE INDEX ao_composer_leases_expiry_idx ON ao_composer_leases (expires_at)
    WHERE holder_user_id IS NOT NULL;

-- People waiting for the lease, served first come, first served.
CREATE TABLE ao_composer_waitlist (
    session_id UUID NOT NULL REFERENCES ao_sessions(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, user_id)
);
CREATE INDEX ao_composer_waitlist_order_idx ON ao_composer_waitlist (session_id, requested_at, user_id);

ALTER TABLE ao_composer_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_composer_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_composer_leases_tenant_policy ON ao_composer_leases
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
-- The expiry sweep finds expired leases across organizations, then advances
-- each inside its own organization's transaction.
CREATE POLICY ao_composer_leases_service_policy ON ao_composer_leases
    USING (ao_service_context()) WITH CHECK (ao_service_context());

ALTER TABLE ao_composer_waitlist ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_composer_waitlist FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_composer_waitlist_tenant_policy ON ao_composer_waitlist
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());

-- A change anyone can see (who holds the lease, or a new grant) wakes the
-- session's live streams. A renewal only moves expires_at and does not.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_composer_lease() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (TG_OP = 'INSERT' AND NEW.holder_user_id IS NOT NULL)
        OR (TG_OP = 'UPDATE' AND OLD.holder_user_id IS DISTINCT FROM NEW.holder_user_id)
        OR (TG_OP = 'UPDATE' AND OLD.epoch <> NEW.epoch) THEN
        PERFORM pg_notify('ao_session_live', NEW.session_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_composer_leases_live_notify
    AFTER INSERT OR UPDATE
    ON ao_composer_leases
    FOR EACH ROW EXECUTE FUNCTION ao_notify_composer_lease();

CREATE TRIGGER ao_composer_waitlist_live_notify
    AFTER INSERT OR DELETE
    ON ao_composer_waitlist
    FOR EACH ROW EXECUTE FUNCTION ao_notify_session_live();

-- +goose Down
DROP TRIGGER IF EXISTS ao_composer_waitlist_live_notify ON ao_composer_waitlist;
DROP TRIGGER IF EXISTS ao_composer_leases_live_notify ON ao_composer_leases;
DROP FUNCTION IF EXISTS ao_notify_composer_lease();
DROP TABLE IF EXISTS ao_composer_waitlist;
DROP TABLE IF EXISTS ao_composer_leases;
