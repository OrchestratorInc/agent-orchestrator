-- +goose Up

-- ao_remote_host_tunnels holds each linked machine's Cloudflare tunnel: one per
-- host, however many accounts registered it, so the machine keeps one fixed
-- address. The run token is encrypted like the host tokens in ao_remote_hosts.
-- released_at is set once the sweep deleted the tunnel of a machine that went
-- offline; the DNS record stays, still naming tunnel_id, and the machine gets a
-- new tunnel under it when it comes back. last_seen_at moves when the machine
-- fetches its token; a machine not seen for 90 days loses the record too.
CREATE TABLE ao_remote_host_tunnels (
    host_id TEXT PRIMARY KEY,
    tunnel_id TEXT NOT NULL CHECK (tunnel_id <> ''),
    hostname TEXT NOT NULL CHECK (hostname <> ''),
    encrypted_run_token BYTEA NOT NULL,
    run_token_nonce BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at TIMESTAMPTZ
);

ALTER TABLE ao_remote_host_tunnels ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_remote_host_tunnels FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_remote_host_tunnels_service_policy ON ao_remote_host_tunnels
    USING (current_setting('ao.service', true) = 'control-plane');

-- +goose Down
DROP TABLE IF EXISTS ao_remote_host_tunnels;
