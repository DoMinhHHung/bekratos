DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM module_releases
        WHERE version <> btrim(version)
           OR version ~ '^[vV]'
           OR version !~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
    ) THEN
        RAISE EXCEPTION 'cannot apply canonical SemVer constraint while non-canonical module releases exist';
    END IF;
    IF EXISTS (
        SELECT module_slug, split_part(version, '+', 1)
        FROM module_releases
        GROUP BY module_slug, split_part(version, '+', 1)
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot apply canonical SemVer constraint while precedence-equivalent releases exist';
    END IF;
END;
$$;

ALTER TABLE module_releases
    ADD CONSTRAINT module_releases_version_canonical CHECK (
        version = btrim(version)
        AND version !~ '^[vV]'
        AND version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
    );

CREATE UNIQUE INDEX module_releases_semver_precedence_unique
    ON module_releases (module_slug, split_part(version, '+', 1));

ALTER TABLE module_releases
    ALTER COLUMN published_at DROP DEFAULT;

ALTER TABLE module_release_dependencies
    DROP CONSTRAINT module_release_dependencies_release_id_fkey,
    ADD CONSTRAINT module_release_dependencies_release_id_fkey
        FOREIGN KEY (release_id) REFERENCES module_releases (id) ON DELETE CASCADE;

ALTER TABLE module_release_conflicts
    DROP CONSTRAINT module_release_conflicts_release_id_fkey,
    ADD CONSTRAINT module_release_conflicts_release_id_fkey
        FOREIGN KEY (release_id) REFERENCES module_releases (id) ON DELETE CASCADE;

CREATE OR REPLACE FUNCTION reject_module_release_mutation()
RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'module releases are immutable after publication';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.published_at IS NOT NULL THEN
        RAISE EXCEPTION 'module releases are immutable after publication';
    END IF;

    IF NEW.published_at IS NOT NULL THEN
        IF btrim(NEW.version) = ''
           OR NEW.version !~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
           OR btrim(NEW.runtime_compat) = ''
           OR NEW.runtime_compat !~ '^[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?([[:space:]]*,?[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?)*([[:space:]]*\|\|[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?([[:space:]]*,?[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?)*[[:space:]]*)*$'
           OR NEW.api_contract_version !~ '^[1-9][0-9]*$'
           OR NEW.metadata IS NULL
           OR jsonb_typeof(NEW.metadata) <> 'object'
           OR EXISTS (
               SELECT 1
               FROM module_release_dependencies d
               WHERE d.release_id = OLD.id
                 AND d.dep_module_slug = OLD.module_slug
           )
           OR EXISTS (
               SELECT 1
               FROM module_release_conflicts c
               WHERE c.release_id = OLD.id
                 AND c.conflict_module_slug = OLD.module_slug
           )
           OR EXISTS (
               SELECT 1
               FROM module_release_dependencies d
               WHERE d.release_id = OLD.id
                 AND (
                     btrim(d.version_constraint) = ''
                     OR d.version_constraint !~ '^[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?([[:space:]]*,?[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?)*([[:space:]]*\|\|[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?([[:space:]]*,?[[:space:]]*(\^|~>|~|>=|<=|!=|>|<|=)?[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?)*[[:space:]]*)*$'
                 )
           ) THEN
            RAISE EXCEPTION 'cannot publish a module release with invalid metadata';
        END IF;
        NEW.published_at := transaction_timestamp();
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION reject_module_release_metadata_mutation()
RETURNS trigger AS $$
DECLARE
    rid uuid;
    pub timestamptz;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.release_id <> OLD.release_id THEN
        RAISE EXCEPTION 'module release metadata cannot be moved between releases';
    END IF;

    IF TG_OP = 'DELETE' THEN
        rid := OLD.release_id;
    ELSE
        rid := NEW.release_id;
    END IF;

    SELECT r.published_at INTO pub
    FROM module_releases r
    WHERE r.id = rid
    FOR UPDATE;

    IF NOT FOUND THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'module release % does not exist', rid;
    END IF;

    IF pub IS NOT NULL THEN
        RAISE EXCEPTION 'module release dependency and conflict metadata are immutable after publication';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION require_published_module_release_pin()
RETURNS trigger AS $$
DECLARE
    release_published_at timestamptz;
BEGIN
    SELECT published_at INTO release_published_at
    FROM module_releases
    WHERE id = NEW.release_id
      AND module_slug = NEW.module_slug
    FOR SHARE;

    IF NOT FOUND OR release_published_at IS NULL THEN
        RAISE EXCEPTION 'environment pins require a published module release';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS module_releases_immutable ON module_releases;
DROP TRIGGER IF EXISTS module_release_dependencies_immutable ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_immutable ON module_release_conflicts;
DROP TRIGGER IF EXISTS module_release_dependencies_no_insert_when_published ON module_release_dependencies;
DROP TRIGGER IF EXISTS module_release_conflicts_no_insert_when_published ON module_release_conflicts;

CREATE TRIGGER module_releases_immutable
    BEFORE UPDATE OR DELETE ON module_releases
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_mutation();

CREATE TRIGGER module_release_dependencies_guard
    BEFORE INSERT OR UPDATE OR DELETE ON module_release_dependencies
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_mutation();

CREATE TRIGGER module_release_conflicts_guard
    BEFORE INSERT OR UPDATE OR DELETE ON module_release_conflicts
    FOR EACH ROW EXECUTE FUNCTION reject_module_release_metadata_mutation();

DROP TRIGGER IF EXISTS environment_module_pins_published_release ON environment_module_pins;
CREATE TRIGGER environment_module_pins_published_release
    BEFORE INSERT OR UPDATE OF release_id, module_slug ON environment_module_pins
    FOR EACH ROW EXECUTE FUNCTION require_published_module_release_pin();
