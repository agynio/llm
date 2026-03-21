ALTER TABLE llm_providers
    ADD COLUMN tenant_id UUID NOT NULL;

ALTER TABLE models
    ADD COLUMN tenant_id UUID NOT NULL;

CREATE INDEX idx_llm_providers_tenant_created ON llm_providers (tenant_id, created_at, id);
CREATE INDEX idx_models_tenant_created ON models (tenant_id, created_at, id);
CREATE INDEX idx_models_tenant_provider_created ON models (tenant_id, llm_provider_id, created_at, id);
