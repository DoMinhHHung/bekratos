CREATE TABLE modules (
    slug         TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE module_releases (
    id                   UUID PRIMARY KEY,
    module_slug          TEXT NOT NULL REFERENCES modules (slug),
    version              TEXT NOT NULL,
    runtime_compat       TEXT NOT NULL,
    api_contract_version TEXT NOT NULL,
    metadata             JSONB NOT NULL DEFAULT '{}'::jsonb,
    published_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT module_releases_slug_version_unique UNIQUE (module_slug, version)
);

CREATE TABLE module_release_dependencies (
    release_id      UUID NOT NULL REFERENCES module_releases (id),
    dep_module_slug TEXT NOT NULL REFERENCES modules (slug),
    version_constraint TEXT NOT NULL,
    required        BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (release_id, dep_module_slug)
);

CREATE TABLE module_release_conflicts (
    release_id           UUID NOT NULL REFERENCES module_releases (id),
    conflict_module_slug TEXT NOT NULL REFERENCES modules (slug),
    PRIMARY KEY (release_id, conflict_module_slug)
);

CREATE TABLE environment_module_pins (
    environment_id UUID NOT NULL REFERENCES project_environments (id),
    module_slug    TEXT NOT NULL REFERENCES modules (slug),
    release_id     UUID NOT NULL REFERENCES module_releases (id),
    pinned_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, module_slug)
);

CREATE INDEX environment_module_pins_env_idx ON environment_module_pins (environment_id);

INSERT INTO modules (slug, display_name, description) VALUES
    ('core', 'Core', 'Platform core primitives'),
    ('auth', 'Auth', 'Project end-user authentication module');

INSERT INTO module_releases (id, module_slug, version, runtime_compat, api_contract_version, metadata) VALUES
    ('01900000-0000-7000-8000-000000000001', 'core', '1.0.0', '>=1.0.0', '1', '{"origin":"first-party"}'::jsonb),
    ('01900000-0000-7000-8000-000000000002', 'auth', '1.0.0', '>=1.0.0', '1', '{"origin":"first-party"}'::jsonb),
    ('01900000-0000-7000-8000-000000000003', 'auth', '1.1.0', '>=1.0.0', '1', '{"origin":"first-party"}'::jsonb);

INSERT INTO module_release_dependencies (release_id, dep_module_slug, version_constraint, required) VALUES
    ('01900000-0000-7000-8000-000000000002', 'core', '^1.0.0', true),
    ('01900000-0000-7000-8000-000000000003', 'core', '^1.0.0', true);
