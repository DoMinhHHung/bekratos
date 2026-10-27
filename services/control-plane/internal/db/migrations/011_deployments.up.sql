CREATE TABLE deployments (
    id                          UUID PRIMARY KEY,
    environment_id              UUID NOT NULL REFERENCES project_environments (id),
    configuration_version_id    UUID NOT NULL REFERENCES environment_configuration_versions (id),
    target_mode                 TEXT NOT NULL DEFAULT 'shared'
                                CHECK (target_mode IN ('shared')),
    provider                    TEXT NOT NULL DEFAULT 'unavailable'
                                CHECK (provider IN ('unavailable')),
    status                      TEXT NOT NULL
                                CHECK (status IN (
                                    'queued', 'provisioning', 'ready', 'failed',
                                    'suspending', 'suspended', 'deleting', 'deleted'
                                )),
    failure_code                TEXT NOT NULL DEFAULT '',
    failure_message             TEXT NOT NULL DEFAULT '',
    attempt_count               INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts                INT NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    idempotency_key             TEXT NOT NULL,
    provider_request_id         TEXT NOT NULL DEFAULT '',
    lease_owner                 TEXT,
    lease_expires_at            TIMESTAMPTZ,
    fencing_token               BIGINT NOT NULL DEFAULT 0,
    next_attempt_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    provider_url                TEXT,
    provider_host               TEXT,
    provider_region             TEXT,
    created_by_identity_user_id UUID NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at                  TIMESTAMPTZ,
    ready_at                    TIMESTAMPTZ,
    finished_at                 TIMESTAMPTZ,
    CONSTRAINT deployments_env_idempotency_unique UNIQUE (environment_id, idempotency_key)
);

CREATE INDEX deployments_environment_created_idx
    ON deployments (environment_id, created_at DESC);

CREATE INDEX deployments_claim_idx
    ON deployments (status, next_attempt_at)
    WHERE status IN ('queued', 'provisioning');
