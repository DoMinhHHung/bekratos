CREATE TABLE api_keys (
    id                          UUID PRIMARY KEY,
    environment_id              UUID NOT NULL REFERENCES project_environments (id),
    key_type                    TEXT NOT NULL CHECK (key_type IN ('publishable', 'server_secret')),
    name                        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    key_prefix                  TEXT NOT NULL CHECK (key_prefix ~ '^(bkr_pub_|bkr_sec_)[0-9a-f-]{36}$'),
    secret_hash                 BYTEA NOT NULL CHECK (octet_length(secret_hash) = 32),
    scopes                      JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(scopes) = 'array'),
    created_by_identity_user_id UUID NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at                TIMESTAMPTZ,
    expires_at                  TIMESTAMPTZ,
    revoked_at                  TIMESTAMPTZ,
    rotation_group_id           UUID NOT NULL,
    rotated_from_key_id         UUID REFERENCES api_keys (id),
    CONSTRAINT api_keys_key_prefix_unique UNIQUE (key_prefix),
    CONSTRAINT api_keys_expiry_after_created CHECK (
        expires_at IS NULL OR expires_at > created_at
    ),
    CONSTRAINT api_keys_revoked_consistency CHECK (
        revoked_at IS NULL OR revoked_at >= created_at
    )
);

CREATE INDEX api_keys_environment_created_idx
    ON api_keys (environment_id, created_at DESC);

CREATE INDEX api_keys_environment_active_idx
    ON api_keys (environment_id)
    WHERE revoked_at IS NULL;

CREATE INDEX api_keys_rotation_group_idx
    ON api_keys (rotation_group_id);
