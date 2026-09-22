-- +goose Up

ALTER TABLE ao_terminal_sessions
    ADD COLUMN authoritative_columns INTEGER NOT NULL DEFAULT 0 CHECK (authoritative_columns BETWEEN 0 AND 65535),
    ADD COLUMN authoritative_rows INTEGER NOT NULL DEFAULT 0 CHECK (authoritative_rows BETWEEN 0 AND 65535),
    ADD COLUMN authoritative_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE ao_terminal_viewers (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    session_id UUID NOT NULL,
    terminal_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('primary', 'secondary')),
    visible BOOLEAN NOT NULL,
    columns INTEGER NOT NULL CHECK (columns BETWEEN 0 AND 65535),
    rows INTEGER NOT NULL CHECK (rows BETWEEN 0 AND 65535),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ao_terminal_viewers_terminal_fk
        FOREIGN KEY (org_id, session_id, terminal_id)
        REFERENCES ao_terminal_sessions(org_id, session_id, id)
        ON DELETE CASCADE
);
CREATE INDEX ao_terminal_viewers_active_idx ON ao_terminal_viewers(terminal_id, expires_at);

ALTER TABLE ao_terminal_viewers ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_terminal_viewers FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_terminal_viewers_tenant_policy ON ao_terminal_viewers
    USING (org_id = ao_current_org_id())
    WITH CHECK (org_id = ao_current_org_id());

-- +goose Down

DROP TABLE ao_terminal_viewers;
ALTER TABLE ao_terminal_sessions
    DROP COLUMN authoritative_revision,
    DROP COLUMN authoritative_rows,
    DROP COLUMN authoritative_columns;
