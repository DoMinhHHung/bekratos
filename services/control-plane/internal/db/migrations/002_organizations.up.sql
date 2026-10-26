CREATE TABLE organizations (
    id                          UUID PRIMARY KEY,
    slug                        TEXT NOT NULL,
    display_name                TEXT NOT NULL,
    status                      TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'archived')),
    created_by_identity_user_id UUID NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at                 TIMESTAMPTZ,
    CONSTRAINT organizations_slug_unique UNIQUE (slug),
    CONSTRAINT organizations_archived_consistency CHECK (
        (status = 'archived' AND archived_at IS NOT NULL)
        OR (status <> 'archived' AND archived_at IS NULL)
    )
);

CREATE TABLE organization_memberships (
    organization_id   UUID NOT NULL REFERENCES organizations (id),
    identity_user_id  UUID NOT NULL,
    role              TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'developer', 'viewer', 'billing')),
    status            TEXT NOT NULL CHECK (status IN ('active', 'removed')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, identity_user_id)
);

CREATE INDEX organization_memberships_user_active_idx
    ON organization_memberships (identity_user_id)
    WHERE status = 'active';

CREATE INDEX organization_memberships_org_active_idx
    ON organization_memberships (organization_id)
    WHERE status = 'active';

CREATE TABLE control_plane_audit_events (
    id              UUID PRIMARY KEY,
    actor_user_id   UUID,
    organization_id UUID,
    event_type      TEXT NOT NULL,
    target_type     TEXT,
    target_id       TEXT,
    request_id      TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX control_plane_audit_events_org_created_idx
    ON control_plane_audit_events (organization_id, created_at DESC);

CREATE INDEX control_plane_audit_events_actor_created_idx
    ON control_plane_audit_events (actor_user_id, created_at DESC);
