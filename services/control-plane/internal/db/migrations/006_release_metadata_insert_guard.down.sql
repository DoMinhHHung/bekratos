DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM module_releases WHERE published_at IS NULL) THEN
        RAISE EXCEPTION 'cannot restore NOT NULL published_at while unpublished drafts exist';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS module_release_dependencies_no_insert_when_published ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_no_insert_when_published ON module_release_conflicts;
DROP FUNCTION IF EXISTS reject_module_release_metadata_insert();

ALTER TABLE module_releases
    ALTER COLUMN published_at SET NOT NULL;
