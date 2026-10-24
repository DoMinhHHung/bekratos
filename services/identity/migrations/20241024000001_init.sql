CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS "user";
CREATE SCHEMA IF NOT EXISTS credential;
CREATE SCHEMA IF NOT EXISTS token;
CREATE SCHEMA IF NOT EXISTS session;
CREATE SCHEMA IF NOT EXISTS jwks;
CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE "user".users (
    id              UUID PRIMARY KEY,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'active', 'suspended', 'deleted')),
    status_before_deletion TEXT CHECK (
        status_before_deletion IS NULL
        OR status_before_deletion IN ('pending', 'active', 'suspended')
    ),
    locale          TEXT,
    timezone        TEXT,
    deletion_requested_at TIMESTAMPTZ,
    purge_after     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_purge_only_when_deleted CHECK (
        (status = 'deleted' AND purge_after IS NOT NULL)
        OR (status <> 'deleted' AND purge_after IS NULL AND deletion_requested_at IS NULL)
    )
);

CREATE TABLE "user".user_profile (
    user_id         UUID PRIMARY KEY REFERENCES "user".users (id) ON DELETE CASCADE,
    full_name       TEXT,
    display_name    TEXT,
    avatar_url      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE "user".user_emails (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    email           CITEXT NOT NULL,
    is_primary      BOOLEAN NOT NULL DEFAULT false,
    verified_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_emails_email_unique UNIQUE (email)
);

CREATE UNIQUE INDEX user_emails_one_primary_per_user
    ON "user".user_emails (user_id)
    WHERE is_primary = true;

CREATE TABLE "user".user_usernames (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    username        CITEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_usernames_username_unique UNIQUE (username),
    CONSTRAINT user_usernames_user_unique UNIQUE (user_id),
    CONSTRAINT user_usernames_format CHECK (username ~ '^[a-z0-9_.]{3,30}$')
);

CREATE TABLE "user".policy_acceptances (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    policy_type     TEXT NOT NULL CHECK (policy_type IN ('terms', 'privacy')),
    version         TEXT NOT NULL,
    document_sha256 TEXT NOT NULL,
    accepted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip              TEXT,
    user_agent      TEXT
);

CREATE INDEX policy_acceptances_user_id_idx ON "user".policy_acceptances (user_id);

CREATE TABLE credential.password_credentials (
    user_id             UUID PRIMARY KEY REFERENCES "user".users (id) ON DELETE CASCADE,
    password_hash       TEXT NOT NULL,
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE credential.password_history (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    password_hash   TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX password_history_user_id_created_at_idx
    ON credential.password_history (user_id, created_at DESC);

CREATE TABLE token.verification_tokens (
    id                      UUID PRIMARY KEY,
    user_id                 UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    purpose                 TEXT NOT NULL CHECK (purpose IN ('email_verify', 'password_reset', 'account_restore')),
    email                   CITEXT NOT NULL,
    expires_at              TIMESTAMPTZ NOT NULL,
    consumed_at             TIMESTAMPTZ,
    revoked_at              TIMESTAMPTZ,
    sent_at                 TIMESTAMPTZ,
    delivery_attempts       INTEGER NOT NULL DEFAULT 0,
    next_attempt_at         TIMESTAMPTZ,
    lease_until             TIMESTAMPTZ,
    last_delivery_error_code TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_tokens_expires_after_created CHECK (expires_at > created_at)
);

CREATE INDEX verification_tokens_active_user_purpose_idx
    ON token.verification_tokens (user_id, purpose)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX verification_tokens_due_delivery_idx
    ON token.verification_tokens (next_attempt_at)
    WHERE consumed_at IS NULL
      AND revoked_at IS NULL
      AND (sent_at IS NULL OR next_attempt_at IS NOT NULL);

CREATE TABLE session.sessions (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES "user".users (id) ON DELETE CASCADE,
    ip              TEXT,
    user_agent      TEXT,
    device_name     TEXT,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_active_user_idx
    ON session.sessions (user_id)
    WHERE revoked_at IS NULL;

CREATE TABLE session.refresh_tokens (
    id              UUID PRIMARY KEY,
    session_id      UUID NOT NULL REFERENCES session.sessions (id) ON DELETE CASCADE,
    parent_id       UUID REFERENCES session.refresh_tokens (id),
    token_hash      TEXT NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT refresh_tokens_token_hash_unique UNIQUE (token_hash)
);

CREATE UNIQUE INDEX refresh_tokens_one_active_per_session
    ON session.refresh_tokens (session_id)
    WHERE used_at IS NULL AND revoked_at IS NULL;

CREATE UNIQUE INDEX refresh_tokens_one_child_per_parent
    ON session.refresh_tokens (parent_id)
    WHERE parent_id IS NOT NULL;

CREATE TABLE jwks.signing_keys (
    id              UUID PRIMARY KEY,
    kid             TEXT NOT NULL,
    alg             TEXT NOT NULL CHECK (alg = 'ES256'),
    public_jwk      JSONB NOT NULL,
    private_key_enc BYTEA NOT NULL,
    key_version     INTEGER NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'active', 'retiring', 'retired')),
    publish_at      TIMESTAMPTZ,
    activated_at    TIMESTAMPTZ,
    retire_at       TIMESTAMPTZ,
    publish_until   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT signing_keys_kid_unique UNIQUE (kid)
);

CREATE UNIQUE INDEX signing_keys_one_active
    ON jwks.signing_keys (status)
    WHERE status = 'active';

CREATE TABLE audit.security_events (
    id              UUID PRIMARY KEY,
    event_type      TEXT NOT NULL,
    user_id         UUID,
    session_id      UUID,
    ip              TEXT,
    user_agent      TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX security_events_user_id_created_at_idx
    ON audit.security_events (user_id, created_at DESC);

CREATE INDEX security_events_event_type_created_at_idx
    ON audit.security_events (event_type, created_at DESC);
