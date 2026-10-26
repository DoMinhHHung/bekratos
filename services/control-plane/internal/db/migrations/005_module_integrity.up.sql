ALTER TABLE module_releases
    ADD CONSTRAINT module_releases_slug_id_unique UNIQUE (module_slug, id);

ALTER TABLE environment_module_pins
    ADD CONSTRAINT environment_module_pins_release_module_fk
    FOREIGN KEY (module_slug, release_id)
    REFERENCES module_releases (module_slug, id);

CREATE TABLE environment_module_pin_state (
    environment_id UUID PRIMARY KEY REFERENCES project_environments (id),
    revision       BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION reject_module_release_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'module releases and dependency metadata are immutable after publication';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER module_releases_immutable
    BEFORE UPDATE OR DELETE ON module_releases
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_mutation();

CREATE TRIGGER module_release_dependencies_immutable
    BEFORE UPDATE OR DELETE ON module_release_dependencies
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_mutation();

CREATE TRIGGER module_release_conflicts_immutable
    BEFORE UPDATE OR DELETE ON module_release_conflicts
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_mutation();
