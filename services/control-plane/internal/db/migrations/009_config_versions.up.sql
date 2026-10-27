CREATE TABLE environment_module_pin_state (
    environment_id UUID PRIMARY KEY REFERENCES project_environments (id),
    revision       BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE environment_configuration_versions (
    id                      UUID PRIMARY KEY,
    environment_id          UUID NOT NULL REFERENCES project_environments (id),
    version_number          BIGINT NOT NULL CHECK (version_number > 0),
    parent_version_id       UUID REFERENCES environment_configuration_versions (id),
    expected_active_version_id UUID,
    schema_revision         BIGINT NOT NULL CHECK (schema_revision >= 0),
    pin_revision            BIGINT NOT NULL CHECK (pin_revision >= 0),
    manifest_format_version INT NOT NULL CHECK (manifest_format_version > 0),
    manifest_canonical      TEXT NOT NULL,
    manifest_sha256         TEXT NOT NULL CHECK (manifest_sha256 ~ '^[a-f0-9]{64}$'),
    compatibility           TEXT NOT NULL CHECK (compatibility IN (
        'compatible', 'migration_required', 'breaking_or_destructive'
    )),
    state                   TEXT NOT NULL CHECK (state IN (
        'validating', 'awaiting_confirmation', 'queued', 'applying',
        'active', 'failed', 'superseded', 'blocked'
    )),
    failure_code            TEXT,
    failure_message         TEXT,
    confirmation_required   BOOLEAN NOT NULL DEFAULT false,
    confirmed_at            TIMESTAMPTZ,
    confirmed_by_user_id    UUID,
    confirmation_hash       TEXT,
    activated_at            TIMESTAMPTZ,
    created_by_user_id      UUID NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    idempotency_key         TEXT,
    CONSTRAINT environment_configuration_versions_env_number_unique
        UNIQUE (environment_id, version_number),
    CONSTRAINT environment_configuration_versions_env_idempotency_unique
        UNIQUE (environment_id, idempotency_key)
);

CREATE UNIQUE INDEX environment_configuration_versions_one_active
    ON environment_configuration_versions (environment_id)
    WHERE state = 'active';

CREATE INDEX environment_configuration_versions_env_created_idx
    ON environment_configuration_versions (environment_id, created_at DESC);

CREATE TABLE environment_configuration_apply_jobs (
    id                      UUID PRIMARY KEY,
    environment_id          UUID NOT NULL REFERENCES project_environments (id),
    configuration_version_id UUID NOT NULL REFERENCES environment_configuration_versions (id),
    state                   TEXT NOT NULL CHECK (state IN (
        'queued', 'leased', 'succeeded', 'failed', 'blocked', 'cancelled'
    )),
    attempt_count           INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts            INT NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    lease_owner             TEXT,
    lease_expires_at        TIMESTAMPTZ,
    fencing_token           BIGINT NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
    next_attempt_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error_code         TEXT,
    last_error_message      TEXT,
    apply_request_id        TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT environment_configuration_apply_jobs_version_unique
        UNIQUE (configuration_version_id)
);

CREATE INDEX environment_configuration_apply_jobs_claim_idx
    ON environment_configuration_apply_jobs (state, next_attempt_at)
    WHERE state IN ('queued', 'leased');

CREATE OR REPLACE FUNCTION prevent_config_version_manifest_mutation()
RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.manifest_canonical IS DISTINCT FROM OLD.manifest_canonical
           OR NEW.manifest_sha256 IS DISTINCT FROM OLD.manifest_sha256
           OR NEW.manifest_format_version IS DISTINCT FROM OLD.manifest_format_version
           OR NEW.environment_id IS DISTINCT FROM OLD.environment_id
           OR NEW.version_number IS DISTINCT FROM OLD.version_number
           OR NEW.schema_revision IS DISTINCT FROM OLD.schema_revision
           OR NEW.pin_revision IS DISTINCT FROM OLD.pin_revision THEN
            RAISE EXCEPTION 'configuration version manifest is immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER environment_configuration_versions_immutable_manifest
    BEFORE UPDATE ON environment_configuration_versions
    FOR EACH ROW
    EXECUTE FUNCTION prevent_config_version_manifest_mutation();

CREATE OR REPLACE FUNCTION enforce_active_configuration_version_ownership()
RETURNS trigger AS $$
BEGIN
    IF NEW.active_configuration_version_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM environment_configuration_versions v
        WHERE v.id = NEW.active_configuration_version_id
          AND v.environment_id = NEW.id
          AND v.state = 'active'
    ) THEN
        RAISE EXCEPTION 'active_configuration_version_id must reference active version of same environment';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER project_environments_active_config_ownership
    BEFORE UPDATE OF active_configuration_version_id ON project_environments
    FOR EACH ROW
    EXECUTE FUNCTION enforce_active_configuration_version_ownership();
