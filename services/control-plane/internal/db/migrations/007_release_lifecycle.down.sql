DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM module_releases WHERE published_at IS NULL) THEN
        RAISE EXCEPTION 'cannot rollback release lifecycle while unpublished drafts exist';
    END IF;
END;
$$;

DROP INDEX IF EXISTS module_releases_semver_precedence_unique;
ALTER TABLE module_releases
    DROP CONSTRAINT IF EXISTS module_releases_version_canonical;

DROP TRIGGER IF EXISTS environment_module_pins_published_release ON environment_module_pins;
DROP FUNCTION IF EXISTS require_published_module_release_pin();

DROP TRIGGER IF EXISTS module_releases_immutable ON module_releases;
DROP TRIGGER IF EXISTS module_release_dependencies_guard ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_guard ON module_release_conflicts;
DROP TRIGGER IF EXISTS module_release_dependencies_immutable ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_immutable ON module_release_conflicts;
DROP TRIGGER IF EXISTS module_release_dependencies_no_insert_when_published ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_no_insert_when_published ON module_release_conflicts;
DROP FUNCTION IF EXISTS reject_module_release_metadata_mutation();

CREATE OR REPLACE FUNCTION reject_module_release_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'module releases and dependency metadata are immutable after publication';
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION reject_module_release_metadata_insert()
RETURNS trigger AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM module_releases r
        WHERE r.id = NEW.release_id
          AND r.published_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'module release dependency and conflict metadata are immutable after publication';
    END IF;
    RETURN NEW;
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

CREATE TRIGGER module_release_dependencies_no_insert_when_published
    BEFORE INSERT ON module_release_dependencies
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_insert();

CREATE TRIGGER module_release_conflicts_no_insert_when_published
    BEFORE INSERT ON module_release_conflicts
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_insert();

ALTER TABLE module_release_dependencies
    DROP CONSTRAINT module_release_dependencies_release_id_fkey,
    ADD CONSTRAINT module_release_dependencies_release_id_fkey
        FOREIGN KEY (release_id) REFERENCES module_releases (id);

ALTER TABLE module_release_conflicts
    DROP CONSTRAINT module_release_conflicts_release_id_fkey,
    ADD CONSTRAINT module_release_conflicts_release_id_fkey
        FOREIGN KEY (release_id) REFERENCES module_releases (id);

ALTER TABLE module_releases
    ALTER COLUMN published_at SET DEFAULT now();
