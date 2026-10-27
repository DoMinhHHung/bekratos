CREATE TABLE environment_schema_drafts (
    environment_id UUID PRIMARY KEY REFERENCES project_environments (id),
    revision       BIGINT NOT NULL DEFAULT 0,
    schema_json    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT environment_schema_drafts_revision_nonneg CHECK (revision >= 0)
);

CREATE INDEX environment_schema_drafts_updated_at_idx
    ON environment_schema_drafts (updated_at DESC);
