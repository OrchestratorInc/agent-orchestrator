-- +goose Up

-- Stripe billing for AO Cloud. An organization subscribes to a plan; the
-- plan's limits are Stripe product metadata cached here (plan_limits) so
-- session creation can enforce them inside its own transaction. A NULL
-- subscription_status means the organization never subscribed.
ALTER TABLE ao_organizations
    ADD COLUMN stripe_customer_id TEXT UNIQUE,
    ADD COLUMN stripe_subscription_id TEXT,
    ADD COLUMN subscription_status TEXT,
    ADD COLUMN plan_limits JSONB CHECK (plan_limits IS NULL OR jsonb_typeof(plan_limits) = 'object'),
    ADD COLUMN billing_period_start TIMESTAMPTZ,
    ADD COLUMN billing_period_end TIMESTAMPTZ,
    -- The weekly usage window starts at this instant and repeats every seven
    -- days. It is set once, when the organization first subscribes.
    ADD COLUMN usage_week_anchor TIMESTAMPTZ,
    -- When the subscription first became past_due, for the grace period.
    ADD COLUMN past_due_since TIMESTAMPTZ;

-- Stripe customer -> organization, for webhooks, which carry no tenant. It is
-- the only billing table the service context reads across organizations;
-- every write after the lookup runs scoped to the organization.
CREATE TABLE ao_stripe_customers (
    stripe_customer_id TEXT PRIMARY KEY CHECK (btrim(stripe_customer_id) <> ''),
    org_id UUID NOT NULL UNIQUE REFERENCES ao_organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ao_stripe_customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_stripe_customers FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_stripe_customers_tenant_policy ON ao_stripe_customers
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
CREATE POLICY ao_stripe_customers_service_policy ON ao_stripe_customers
    USING (ao_service_context()) WITH CHECK (ao_service_context());

-- Webhook events already processed. Stripe retries and may deliver an event
-- twice; the first insert wins.
CREATE TABLE ao_stripe_events (
    event_id TEXT PRIMARY KEY CHECK (btrim(event_id) <> ''),
    type TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ao_stripe_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_stripe_events FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_stripe_events_service_policy ON ao_stripe_events
    USING (ao_service_context()) WITH CHECK (ao_service_context());

-- One row per session per minute its sandbox was running: the usage ledger
-- the 5-hour and weekly limits are measured against. A sampler inserts the
-- current minute for every running sandbox; the primary key makes samples
-- from several control-plane replicas, or a retried sample, count once.
CREATE TABLE ao_usage_minutes (
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    minute TIMESTAMPTZ NOT NULL,
    session_id UUID NOT NULL,
    PRIMARY KEY (org_id, minute, session_id)
);
ALTER TABLE ao_usage_minutes ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_usage_minutes FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_usage_minutes_tenant_policy ON ao_usage_minutes
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());
CREATE POLICY ao_usage_minutes_service_policy ON ao_usage_minutes
    USING (ao_service_context()) WITH CHECK (ao_service_context());

-- Manual resets of the weekly usage limit. A reset starts a fresh week now;
-- the plan allows a fixed number per billing period.
CREATE TABLE ao_usage_resets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES ao_organizations(id) ON DELETE CASCADE,
    reset_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id UUID
);
CREATE INDEX ao_usage_resets_org_idx ON ao_usage_resets (org_id, reset_at DESC);
ALTER TABLE ao_usage_resets ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_usage_resets FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_usage_resets_tenant_policy ON ao_usage_resets
    USING (org_id = ao_current_org_id()) WITH CHECK (org_id = ao_current_org_id());

-- The last time a person interacted with a session (terminal input or a
-- visible desktop view). Idle pause measures quiet time from the latest of
-- this and the last chat message, so typing in a terminal keeps a session
-- awake for the whole idle threshold, not only the short interaction lease.
ALTER TABLE ao_sandboxes ADD COLUMN last_interaction_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_sandboxes DROP COLUMN last_interaction_at;
DROP TABLE ao_usage_resets;
DROP TABLE ao_usage_minutes;
DROP TABLE ao_stripe_events;
DROP TABLE ao_stripe_customers;
ALTER TABLE ao_organizations
    DROP COLUMN past_due_since,
    DROP COLUMN usage_week_anchor,
    DROP COLUMN billing_period_end,
    DROP COLUMN billing_period_start,
    DROP COLUMN plan_limits,
    DROP COLUMN subscription_status,
    DROP COLUMN stripe_subscription_id,
    DROP COLUMN stripe_customer_id;
