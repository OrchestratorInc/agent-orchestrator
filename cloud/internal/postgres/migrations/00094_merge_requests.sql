-- +goose Up

-- A guest's request that a shared project's owner merge a pull request. A
-- guest cannot merge themselves; the owner (the session's creator, or an
-- owner or admin of the organization) approves, which merges, or rejects.
-- A pull request has at most one pending request.
CREATE TABLE ao_merge_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    session_id UUID NOT NULL,
    pull_request_id UUID NOT NULL,
    requested_by_user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'merged', 'rejected')),
    decided_by_user_id UUID REFERENCES ao_users(id) ON DELETE SET NULL,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ao_merge_requests_session_fk
        FOREIGN KEY (org_id, project_id, session_id)
        REFERENCES ao_sessions(org_id, project_id, id) ON DELETE CASCADE,
    CONSTRAINT ao_merge_requests_pull_request_fk
        FOREIGN KEY (org_id, pull_request_id)
        REFERENCES ao_pull_requests(org_id, id) ON DELETE CASCADE,
    CONSTRAINT ao_merge_requests_decided CHECK ((status = 'pending') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX ao_merge_requests_one_pending
    ON ao_merge_requests (org_id, pull_request_id) WHERE status = 'pending';
CREATE INDEX ao_merge_requests_project_idx ON ao_merge_requests (org_id, project_id, created_at DESC);

ALTER TABLE ao_merge_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_merge_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_merge_requests_tenant_policy ON ao_merge_requests
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());

-- A request asked for or decided wakes the live streams of its session and
-- of its project's orchestrator, where the owner decides it, so both see it
-- at once.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_merge_request_live() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    orchestrator UUID;
BEGIN
    PERFORM pg_notify('ao_session_live', NEW.session_id::text);
    FOR orchestrator IN
        SELECT id FROM ao_sessions
        WHERE org_id = NEW.org_id AND project_id = NEW.project_id
          AND kind = 'orchestrator' AND is_terminated = false AND id <> NEW.session_id
    LOOP
        PERFORM pg_notify('ao_session_live', orchestrator::text);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_merge_requests_live_notify
    AFTER INSERT OR UPDATE
    ON ao_merge_requests
    FOR EACH ROW EXECUTE FUNCTION ao_notify_merge_request_live();

-- +goose Down
DROP TRIGGER IF EXISTS ao_merge_requests_live_notify ON ao_merge_requests;
DROP FUNCTION IF EXISTS ao_notify_merge_request_live();
DROP TABLE IF EXISTS ao_merge_requests;
