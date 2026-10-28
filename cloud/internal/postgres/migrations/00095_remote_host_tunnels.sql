-- +goose Up

-- ao_remote_host_tunnels holds the Cloudflare tunnel that gives a linked
-- machine its fixed address in one account. Each account that lists a host has
-- its own tunnel and address, so an account that lists someone else's host ID
-- gets a tunnel no device of theirs uses. The run token is encrypted like the
-- host tokens in ao_remote_hosts. created_at is when the DNS record was made:
-- the machine holds a newer address back from devices until resolvers can have
-- it. released_at is set once the sweep deleted the tunnel of a machine that
-- went offline; the DNS record stays, still naming tunnel_id, and the machine
-- gets a new tunnel under it when it comes back. last_seen_at moves when the
-- machine fetches its token; a machine not seen for 90 days loses the record
-- too. linked_at is when the account last linked the host to the tunnel, NULL
-- for a tunnel the machine's own fetch made; for a while after a link, the
-- listing keeps the fixed address. No foreign keys: a row outlives its registration until its tunnel is
-- deleted.
CREATE TABLE ao_remote_host_tunnels (
    user_id UUID NOT NULL,
    host_id TEXT NOT NULL,
    tunnel_id TEXT NOT NULL UNIQUE CHECK (tunnel_id <> ''),
    hostname TEXT NOT NULL CHECK (hostname <> ''),
    encrypted_run_token BYTEA NOT NULL,
    run_token_nonce BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, host_id)
);

ALTER TABLE ao_remote_host_tunnels ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_remote_host_tunnels FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_remote_host_tunnels_service_policy ON ao_remote_host_tunnels
    USING (current_setting('ao.service', true) = 'control-plane');

-- +goose Down
DROP TABLE IF EXISTS ao_remote_host_tunnels;
