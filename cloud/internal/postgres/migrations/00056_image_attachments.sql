-- +goose Up
CREATE TABLE ao_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    session_id UUID,
    created_by_user_id UUID NOT NULL REFERENCES ao_users(id),
    idempotency_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 10485760),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/png','image/jpeg','image/gif','image/webp','image/bmp')),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ready','expired')),
    upload_expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '10 minutes',
    upload_cleaned BOOLEAN NOT NULL DEFAULT false,
    retained BOOLEAN NOT NULL DEFAULT false,
    preparation_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours',
    UNIQUE (org_id,id),
    UNIQUE (org_id,created_by_user_id,idempotency_key),
    FOREIGN KEY (org_id,project_id) REFERENCES ao_projects(org_id,id) ON DELETE CASCADE,
    FOREIGN KEY (org_id,session_id) REFERENCES ao_sessions(org_id,id) ON DELETE CASCADE
);
CREATE TABLE ao_message_attachments (
    org_id UUID NOT NULL,
    session_id UUID NOT NULL,
    event_sequence BIGINT NOT NULL,
    attachment_id UUID NOT NULL,
    PRIMARY KEY (org_id,session_id,event_sequence,attachment_id),
    FOREIGN KEY (org_id,attachment_id) REFERENCES ao_attachments(org_id,id) ON DELETE CASCADE,
    FOREIGN KEY (org_id,session_id,event_sequence) REFERENCES ao_events(org_id,session_id,sequence) ON DELETE CASCADE
);
CREATE INDEX ao_attachments_session ON ao_attachments(org_id,session_id) WHERE retained;
CREATE INDEX ao_attachments_cleanup ON ao_attachments(expires_at) WHERE NOT retained;
ALTER TABLE ao_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_attachments_tenant ON ao_attachments
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
CREATE POLICY ao_attachments_service ON ao_attachments
    USING (ao_service_context()) WITH CHECK (ao_service_context());
ALTER TABLE ao_message_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_message_attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_message_attachments_tenant ON ao_message_attachments
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
ALTER TABLE ao_worker_requests DROP CONSTRAINT ao_worker_requests_kind_check;
ALTER TABLE ao_worker_requests ADD CONSTRAINT ao_worker_requests_kind_check CHECK (kind IN (
    'workspace.list', 'workspace.read', 'workspace.write', 'workspace.diff',
    'workspace.diff-file', 'workspace.review.summary', 'workspace.review.tree',
    'workspace.review.search', 'workspace.review.file', 'workspace.review.diffs',
    'workspace.review.revision', 'workspace.review.write',
    'terminal.open', 'terminal.input', 'terminal.resize', 'terminal.close',
    'browser.fetch', 'interface.inspect', 'interface.interrupt', 'interface.stop',
    'interface.native-id', 'interface.start', 'interface.ready', 'chat.models',
    'chat.steer', 'harness.inspect', 'harness.install', 'attachments.materialize'
)) NOT VALID;

-- +goose Down
DELETE FROM ao_worker_requests WHERE kind = 'attachments.materialize';
ALTER TABLE ao_worker_requests DROP CONSTRAINT ao_worker_requests_kind_check;
ALTER TABLE ao_worker_requests ADD CONSTRAINT ao_worker_requests_kind_check CHECK (kind IN (
    'workspace.list', 'workspace.read', 'workspace.write', 'workspace.diff',
    'workspace.diff-file', 'workspace.review.summary', 'workspace.review.tree',
    'workspace.review.search', 'workspace.review.file', 'workspace.review.diffs',
    'workspace.review.revision', 'workspace.review.write',
    'terminal.open', 'terminal.input', 'terminal.resize', 'terminal.close',
    'browser.fetch', 'interface.inspect', 'interface.interrupt', 'interface.stop',
    'interface.native-id', 'interface.start', 'interface.ready', 'chat.models',
    'chat.steer', 'harness.inspect', 'harness.install'
)) NOT VALID;
DROP TABLE ao_message_attachments;
DROP TABLE ao_attachments;
