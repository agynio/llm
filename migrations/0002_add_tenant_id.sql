ALTER TABLE llm_providers
    ADD COLUMN tenant_id UUID NOT NULL;

ALTER TABLE models
    ADD COLUMN tenant_id UUID NOT NULL;

CREATE INDEX llm_providers_tenant_id_idx ON llm_providers (tenant_id);
CREATE INDEX models_tenant_id_idx ON models (tenant_id);
