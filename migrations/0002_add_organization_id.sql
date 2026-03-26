ALTER TABLE llm_providers ADD COLUMN organization_id UUID NOT NULL;
ALTER TABLE models ADD COLUMN organization_id UUID NOT NULL;

CREATE INDEX idx_llm_providers_organization_created ON llm_providers (organization_id, created_at, id);
CREATE INDEX idx_models_organization_created ON models (organization_id, created_at, id);
CREATE INDEX idx_models_organization_provider_created ON models (organization_id, llm_provider_id, created_at, id);
