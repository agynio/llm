ALTER TABLE llm_providers ADD COLUMN organization_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
ALTER TABLE llm_providers ALTER COLUMN organization_id DROP DEFAULT;

ALTER TABLE models ADD COLUMN organization_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
ALTER TABLE models ALTER COLUMN organization_id DROP DEFAULT;

CREATE INDEX idx_llm_providers_organization_created ON llm_providers (organization_id, created_at, id);
CREATE INDEX idx_models_organization_created ON models (organization_id, created_at, id);
CREATE INDEX idx_models_organization_provider_created ON models (organization_id, llm_provider_id, created_at, id);
