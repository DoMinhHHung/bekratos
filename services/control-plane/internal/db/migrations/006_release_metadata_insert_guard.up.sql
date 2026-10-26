ALTER TABLE module_releases
    ALTER COLUMN published_at DROP NOT NULL;

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

CREATE TRIGGER module_release_dependencies_no_insert_when_published
    BEFORE INSERT ON module_release_dependencies
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_insert();

CREATE TRIGGER module_release_conflicts_no_insert_when_published
    BEFORE INSERT ON module_release_conflicts
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_insert();
