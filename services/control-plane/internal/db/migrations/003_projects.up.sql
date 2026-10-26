CREATE TABLE projects (
    id                          UUID PRIMARY KEY,
    organization_id             UUID NOT NULL REFERENCES organizations (id),
    slug                        TEXT NOT NULL,
    display_name                TEXT NOT NULL,
    description                 TEXT,
    status                      TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'archived')),
    created_by_identity_user_id UUID NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at                 TIMESTAMPTZ,
    CONSTRAINT projects_org_slug_unique UNIQUE (organization_id, slug),
    CONSTRAINT projects_archived_consistency CHECK (
        (status = 'archived' AND archived_at IS NOT NULL)
        OR (status <> 'archived' AND archived_at IS NULL)
    )
);

CREATE INDEX projects_organization_id_idx ON projects (organization_id);

CREATE TABLE project_environments (
    id                              UUID PRIMARY KEY,
    project_id                      UUID NOT NULL REFERENCES projects (id),
    slug                            TEXT NOT NULL,
    kind                            TEXT NOT NULL CHECK (kind IN ('development', 'staging', 'production', 'custom')),
    status                          TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'archived')),
    active_configuration_version_id UUID,
    active_deployment_id            UUID,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at                     TIMESTAMPTZ,
    CONSTRAINT project_environments_project_slug_unique UNIQUE (project_id, slug),
    CONSTRAINT project_environments_archived_consistency CHECK (
        (status = 'archived' AND archived_at IS NOT NULL)
        OR (status <> 'archived' AND archived_at IS NULL)
    )
);

CREATE INDEX project_environments_project_id_idx ON project_environments (project_id);
