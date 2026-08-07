-- A credential for a vendor's own consumer plan, used by an agent CLI running in
-- its native configuration. The token is held by reference to a Secret rather
-- than inline like llm_providers.token, so it can live in Vault and rotate in
-- one place.
CREATE TABLE subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    name TEXT NOT NULL,
    -- Immutable: changing it would silently redirect every workload attached.
    vendor TEXT NOT NULL,
    secret_id UUID NOT NULL,
    account_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_vendor_check CHECK (vendor IN ('claude', 'codex')),
    CONSTRAINT subscriptions_name_unique UNIQUE (organization_id, name)
);

CREATE INDEX idx_subscriptions_organization_created ON subscriptions (organization_id, created_at, id);
-- Backs CountSubscriptionsReferencingSecret, which Secrets calls before delete.
CREATE INDEX idx_subscriptions_secret ON subscriptions (secret_id);

-- Binds a subscription to an agent or an environment. Immutable: create and
-- delete only.
CREATE TABLE subscription_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    subscription_id UUID NOT NULL REFERENCES subscriptions(id),
    -- Denormalized from the subscription so uniqueness on (vendor, target) is a
    -- plain index rather than a constraint spanning a join. Safe because
    -- subscriptions.vendor is immutable.
    vendor TEXT NOT NULL,
    agent_id UUID,
    environment_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT subscription_attachments_target_check CHECK (
        (agent_id IS NOT NULL)::int + (environment_id IS NOT NULL)::int = 1
    )
);

-- The load-bearing constraint: an intercepted request carries nothing that
-- identifies a credential, so (caller, vendor) must resolve to exactly one.
-- Two partial indexes because a target is either an agent or an environment.
CREATE UNIQUE INDEX subscription_attachments_agent_vendor_unique
    ON subscription_attachments (agent_id, vendor) WHERE agent_id IS NOT NULL;
CREATE UNIQUE INDEX subscription_attachments_environment_vendor_unique
    ON subscription_attachments (environment_id, vendor) WHERE environment_id IS NOT NULL;

CREATE INDEX idx_subscription_attachments_subscription ON subscription_attachments (subscription_id);
CREATE INDEX idx_subscription_attachments_organization_created ON subscription_attachments (organization_id, created_at, id);
