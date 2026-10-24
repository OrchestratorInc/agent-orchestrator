-- +goose Up

CREATE TABLE ao_agent_login_attempts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES ao_users(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    harness TEXT NOT NULL CHECK (harness IN ('claude-code', 'codex')),
    status TEXT NOT NULL DEFAULT 'starting'
        CHECK (status IN ('starting','waiting','connecting','connected','failed','cancelled','expired')),
    idempotency_key UUID NOT NULL,
    runner_token_hash BYTEA NOT NULL CHECK (octet_length(runner_token_hash) = 32),
    authorization_url TEXT NOT NULL DEFAULT '',
    user_code TEXT NOT NULL DEFAULT '',
    requires_code BOOLEAN NOT NULL DEFAULT false,
    input_ciphertext BYTEA,
    input_nonce BYTEA,
    job_id TEXT NOT NULL DEFAULT '',
    launch_finished BOOLEAN NOT NULL DEFAULT false,
    cleanup_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key),
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '10 minutes'),
    CHECK ((input_ciphertext IS NULL AND input_nonce IS NULL)
        OR (input_ciphertext IS NOT NULL AND input_nonce IS NOT NULL
            AND octet_length(input_ciphertext) > 0 AND octet_length(input_nonce) = 12))
);

CREATE UNIQUE INDEX ao_agent_login_attempts_active_user_harness_idx
    ON ao_agent_login_attempts(user_id,harness)
    WHERE status IN ('starting','waiting','connecting');
CREATE INDEX ao_agent_login_attempts_cleanup_idx ON ao_agent_login_attempts(cleanup_after,expires_at)
    WHERE job_id <> '';

ALTER TABLE ao_agent_login_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_agent_login_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_agent_login_attempts_owner_policy ON ao_agent_login_attempts
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());
-- Background cleanup and hashed runner routing may locate private rows; all
-- mutations still use their owner's RLS identity.
CREATE POLICY ao_agent_login_attempts_service_read_policy ON ao_agent_login_attempts
    FOR SELECT USING (current_setting('ao.service',true) = 'control-plane');

-- Every direct personal credential write supersedes pending native logins.
-- A shared advisory lock serializes this with login creation/promotion, even
-- when the personal connection does not exist yet. Only the atomic promotion
-- transaction exempts its own attempt from cancellation.
-- +goose StatementBegin
CREATE FUNCTION ao_supersede_agent_logins() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    owner_id UUID;
    provider_name TEXT;
    connection_label TEXT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        owner_id := OLD.user_id;
        provider_name := OLD.provider;
        connection_label := OLD.label;
    ELSE
        owner_id := NEW.user_id;
        provider_name := NEW.provider;
        connection_label := NEW.label;
    END IF;
    IF connection_label = 'default' AND provider_name IN ('claude-code','codex') THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('agent-login:' || owner_id::text || ':' || provider_name,0));
        UPDATE ao_agent_login_attempts
        SET status = 'cancelled', input_ciphertext = NULL, input_nonce = NULL,
            authorization_url = '', user_code = '', requires_code = false, updated_at = now()
        WHERE user_id = owner_id AND harness = provider_name
          AND status IN ('starting','waiting','connecting')
          AND id::text IS DISTINCT FROM nullif(current_setting('ao.agent_login_id',true),'');
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER ao_user_provider_connections_supersede_logins
    BEFORE INSERT OR UPDATE OR DELETE ON ao_user_provider_connections
    FOR EACH ROW EXECUTE FUNCTION ao_supersede_agent_logins();

-- +goose Down
DROP TRIGGER IF EXISTS ao_user_provider_connections_supersede_logins ON ao_user_provider_connections;
DROP FUNCTION IF EXISTS ao_supersede_agent_logins();
DROP TABLE IF EXISTS ao_agent_login_attempts;
