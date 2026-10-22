-- +goose Up
CREATE TABLE ao_remote_hosts (
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    host_id TEXT NOT NULL,
    label TEXT NOT NULL CHECK (btrim(label) <> ''),
    url TEXT NOT NULL,
    encrypted_token BYTEA NOT NULL,
    token_nonce BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, host_id)
);

CREATE INDEX ao_remote_hosts_host_id_idx ON ao_remote_hosts (host_id);

ALTER TABLE ao_remote_hosts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_remote_hosts FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_remote_hosts_owner_policy ON ao_remote_hosts
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());
CREATE POLICY ao_remote_hosts_service_policy ON ao_remote_hosts
    USING (current_setting('ao.service', true) = 'control-plane');

-- +goose Down
DROP TABLE IF EXISTS ao_remote_hosts;
