package module

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConflictRevision        = errors.New("revision conflict")
	ErrInvalidRelease          = errors.New("invalid release metadata")
	ErrReleaseAlreadyPublished = errors.New("release already published")
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) ListModules(ctx context.Context) ([]Module, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT slug, display_name, description, created_at
		FROM modules m
		WHERE EXISTS (
			SELECT 1 FROM module_releases r
			WHERE r.module_slug = m.slug AND r.published_at IS NOT NULL
		)
		ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Module
	for rows.Next() {
		var m Module
		if err := rows.Scan(&m.Slug, &m.DisplayName, &m.Description, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repository) ListPublishedReleases(ctx context.Context, moduleSlug string) ([]Release, error) {
	return r.ListReleases(ctx, moduleSlug)
}

func (r *Repository) ListReleases(ctx context.Context, moduleSlug string) ([]Release, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		FROM module_releases
		WHERE published_at IS NOT NULL
		  AND ($1 = '' OR module_slug = $1)
		ORDER BY module_slug, version DESC`, moduleSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var rel Release
		if err := rows.Scan(&rel.ID, &rel.ModuleSlug, &rel.Version, &rel.RuntimeCompat,
			&rel.APIContractVersion, &rel.Metadata, &rel.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachDepsAndConflicts(ctx, out); err != nil {
		return nil, err
	}
	sortReleasesSemver(out)
	return out, nil
}

func (r *Repository) CreateDraft(ctx context.Context, release Release) error {
	if release.ID == uuid.Nil || release.PublishedAt != nil || validateReleaseMetadata(release) != nil {
		return ErrInvalidRelease
	}
	if release.Metadata == nil {
		release.Metadata = []byte("{}")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO module_releases (
			id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL)`,
		release.ID, release.ModuleSlug, release.Version, release.RuntimeCompat,
		release.APIContractVersion, release.Metadata)
	if err != nil {
		return err
	}
	for _, dependency := range release.Dependencies {
		_, err = tx.Exec(ctx, `
			INSERT INTO module_release_dependencies (
				release_id, dep_module_slug, version_constraint, required
			) VALUES ($1, $2, $3, $4)`,
			release.ID, dependency.ModuleSlug, dependency.VersionConstraint, dependency.Required)
		if err != nil {
			return err
		}
	}
	for _, conflict := range release.Conflicts {
		_, err = tx.Exec(ctx, `
			INSERT INTO module_release_conflicts (release_id, conflict_module_slug)
			VALUES ($1, $2)`, release.ID, conflict)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) PublishRelease(ctx context.Context, releaseID uuid.UUID) (time.Time, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)

	var release Release
	err = tx.QueryRow(ctx, `
		SELECT id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		FROM module_releases
		WHERE id = $1
		FOR UPDATE`, releaseID,
	).Scan(&release.ID, &release.ModuleSlug, &release.Version, &release.RuntimeCompat,
		&release.APIContractVersion, &release.Metadata, &release.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	if release.PublishedAt != nil {
		return time.Time{}, ErrReleaseAlreadyPublished
	}

	release.Dependencies, err = loadDependenciesTx(ctx, tx, release.ID)
	if err != nil {
		return time.Time{}, err
	}
	release.Conflicts, err = loadConflictsTx(ctx, tx, release.ID)
	if err != nil {
		return time.Time{}, err
	}
	if err := validateReleaseMetadata(release); err != nil {
		return time.Time{}, err
	}

	var publishedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE module_releases SET published_at = now()
		WHERE id = $1 AND published_at IS NULL
		RETURNING published_at`, release.ID,
	).Scan(&publishedAt)
	if err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return publishedAt, nil
}

func validateReleaseMetadata(release Release) error {
	version, err := ParseVersion(release.Version)
	if err != nil || version.String() != release.Version {
		return ErrInvalidRelease
	}
	if strings.TrimSpace(release.ModuleSlug) == "" || strings.TrimSpace(release.RuntimeCompat) != release.RuntimeCompat {
		return ErrInvalidRelease
	}
	if _, err := ParseConstraint(release.RuntimeCompat); err != nil {
		return ErrInvalidRelease
	}
	apiVersion, err := strconv.ParseUint(release.APIContractVersion, 10, 32)
	if err != nil || apiVersion == 0 || strconv.FormatUint(apiVersion, 10) != release.APIContractVersion {
		return ErrInvalidRelease
	}
	if len(release.Metadata) > 0 {
		var metadata map[string]any
		if err := json.Unmarshal(release.Metadata, &metadata); err != nil || metadata == nil {
			return ErrInvalidRelease
		}
	}
	for _, dependency := range release.Dependencies {
		if strings.TrimSpace(dependency.ModuleSlug) == "" || dependency.ModuleSlug == release.ModuleSlug {
			return ErrInvalidRelease
		}
		if _, err := ParseConstraint(dependency.VersionConstraint); err != nil {
			return ErrInvalidRelease
		}
	}
	for _, conflict := range release.Conflicts {
		if strings.TrimSpace(conflict) == "" || conflict == release.ModuleSlug {
			return ErrInvalidRelease
		}
	}
	return nil
}

func loadDependenciesTx(ctx context.Context, tx pgx.Tx, releaseID uuid.UUID) ([]Dependency, error) {
	rows, err := tx.Query(ctx, `
		SELECT dep_module_slug, version_constraint, required
		FROM module_release_dependencies
		WHERE release_id = $1
		ORDER BY dep_module_slug`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dependencies []Dependency
	for rows.Next() {
		var dependency Dependency
		if err := rows.Scan(&dependency.ModuleSlug, &dependency.VersionConstraint, &dependency.Required); err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, rows.Err()
}

func loadConflictsTx(ctx context.Context, tx pgx.Tx, releaseID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT conflict_module_slug
		FROM module_release_conflicts
		WHERE release_id = $1
		ORDER BY conflict_module_slug`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conflicts []string
	for rows.Next() {
		var conflict string
		if err := rows.Scan(&conflict); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func sortReleasesSemver(releases []Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].ModuleSlug != releases[j].ModuleSlug {
			return releases[i].ModuleSlug < releases[j].ModuleSlug
		}
		return releaseLess(releases[j], releases[i])
	})
}

func (r *Repository) attachDepsAndConflicts(ctx context.Context, releases []Release) error {
	if len(releases) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(releases))
	index := make(map[uuid.UUID]int, len(releases))
	for i, rel := range releases {
		ids[i] = rel.ID
		index[rel.ID] = i
		releases[i].Dependencies = nil
		releases[i].Conflicts = nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT release_id, dep_module_slug, version_constraint, required
		FROM module_release_dependencies
		WHERE release_id = ANY($1)
		ORDER BY release_id, dep_module_slug`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var d Dependency
		if err := rows.Scan(&id, &d.ModuleSlug, &d.VersionConstraint, &d.Required); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			releases[i].Dependencies = append(releases[i].Dependencies, d)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	crows, err := r.pool.Query(ctx, `
		SELECT release_id, conflict_module_slug
		FROM module_release_conflicts
		WHERE release_id = ANY($1)
		ORDER BY release_id, conflict_module_slug`, ids)
	if err != nil {
		return err
	}
	defer crows.Close()
	for crows.Next() {
		var id uuid.UUID
		var slug string
		if err := crows.Scan(&id, &slug); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			releases[i].Conflicts = append(releases[i].Conflicts, slug)
		}
	}
	return crows.Err()
}

func (r *Repository) loadDeps(ctx context.Context, releaseID uuid.UUID) ([]Dependency, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT dep_module_slug, version_constraint, required
		FROM module_release_dependencies WHERE release_id = $1 ORDER BY dep_module_slug`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.ModuleSlug, &d.VersionConstraint, &d.Required); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) loadConflicts(ctx context.Context, releaseID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT conflict_module_slug FROM module_release_conflicts
		WHERE release_id = $1 ORDER BY conflict_module_slug`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) ListPinsAuthorized(ctx context.Context, orgID, projectID, envID, actorUserID uuid.UUID) ([]Pin, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	var orgStatus string
	var role string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1
		  AND m.identity_user_id = $2
		  AND m.status = 'active'
		FOR SHARE OF o, m`, orgID, actorUserID,
	).Scan(&orgStatus, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	parsed, ok := org.ParseRole(role)
	if !ok || !parsed.CanReadEnvironment() {
		return nil, 0, ErrForbidden
	}
	if orgStatus != "active" {
		return nil, 0, ErrInactive
	}

	var projectStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM projects
		WHERE id = $1 AND organization_id = $2
		FOR SHARE`, projectID, orgID,
	).Scan(&projectStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	if projectStatus != "active" {
		return nil, 0, ErrInactive
	}

	var environmentStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM project_environments
		WHERE id = $1 AND project_id = $2
		FOR SHARE`, envID, projectID,
	).Scan(&environmentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	if environmentStatus != "active" {
		return nil, 0, ErrInactive
	}

	pins, err := r.listPinsTx(ctx, tx, envID)
	if err != nil {
		return nil, 0, err
	}
	var revision int64
	err = tx.QueryRow(ctx, `
		SELECT revision FROM environment_module_pin_state WHERE environment_id = $1`, envID,
	).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		revision = 0
	} else if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return pins, revision, nil
}

func (r *Repository) ReplacePins(ctx context.Context, envID uuid.UUID, pins []Pin, expectedRevision *int64, actorUserID *uuid.UUID, orgID *uuid.UUID, requestID string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var rev int64
	_, err = tx.Exec(ctx, `
		INSERT INTO environment_module_pin_state (environment_id, revision, updated_at)
		VALUES ($1, 0, now())
		ON CONFLICT (environment_id) DO NOTHING`, envID)
	if err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx, `
		SELECT revision FROM environment_module_pin_state
		WHERE environment_id = $1 FOR UPDATE`, envID,
	).Scan(&rev)
	if err != nil {
		return 0, err
	}

	if expectedRevision != nil && *expectedRevision != rev {
		return 0, ErrConflictRevision
	}

	before, err := r.listPinsTx(ctx, tx, envID)
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM environment_module_pins WHERE environment_id = $1`, envID); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, p := range pins {
		if _, err := tx.Exec(ctx, `
			INSERT INTO environment_module_pins (environment_id, module_slug, release_id, pinned_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)`,
			envID, p.ModuleSlug, p.ReleaseID, now, now,
		); err != nil {
			return 0, err
		}
	}

	newRev := rev + 1
	if _, err := tx.Exec(ctx, `
		UPDATE environment_module_pin_state
		SET revision = $2, updated_at = now()
		WHERE environment_id = $1`, envID, newRev); err != nil {
		return 0, err
	}

	afterMeta := make([]map[string]string, 0, len(pins))
	for _, p := range pins {
		afterMeta = append(afterMeta, map[string]string{
			"module_slug": p.ModuleSlug,
			"version":     p.Version,
			"release_id":  p.ReleaseID.String(),
		})
	}
	beforeMeta := make([]map[string]string, 0, len(before))
	for _, p := range before {
		beforeMeta = append(beforeMeta, map[string]string{
			"module_slug": p.ModuleSlug,
			"version":     p.Version,
			"release_id":  p.ReleaseID.String(),
		})
	}
	meta, _ := json.Marshal(map[string]any{
		"before":   beforeMeta,
		"after":    afterMeta,
		"revision": newRev,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO control_plane_audit_events (
			id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		uuid.Must(uuid.NewV7()), actorUserID, orgID, "environment.modules.pinned", "environment", envID.String(), requestID, meta,
	); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newRev, nil
}

func (r *Repository) listPinsTx(ctx context.Context, tx pgx.Tx, envID uuid.UUID) ([]Pin, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.environment_id, p.module_slug, p.release_id, r.version, p.pinned_at, p.updated_at
		FROM environment_module_pins p
		INNER JOIN module_releases r ON r.id = p.release_id AND r.module_slug = p.module_slug
		                           AND r.published_at IS NOT NULL
		                           AND r.published_at IS NOT NULL
		WHERE p.environment_id = $1
		ORDER BY p.module_slug`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pin
	for rows.Next() {
		var p Pin
		if err := rows.Scan(&p.EnvironmentID, &p.ModuleSlug, &p.ReleaseID, &p.Version, &p.PinnedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) ReplacePinsAuthorized(ctx context.Context, orgID, projectID, envID uuid.UUID, pins []Pin, expectedRevision *int64, actorUserID uuid.UUID, requestID string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var role string
	var orgStatus string
	err = tx.QueryRow(ctx, `
		SELECT m.role, o.status
		FROM organization_memberships m
		INNER JOIN organizations o ON o.id = m.organization_id
		WHERE m.organization_id = $1
		  AND m.identity_user_id = $2
		  AND m.status = 'active'
		FOR UPDATE OF m, o`, orgID, actorUserID,
	).Scan(&role, &orgStatus)
	if err != nil {
		return 0, ErrNotFound
	}
	if orgStatus != "active" {
		return 0, ErrInactive
	}
	parsed, ok := org.ParseRole(role)
	if !ok || !parsed.CanUpdateProject() {
		return 0, ErrForbidden
	}

	var projectStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM projects
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE`, projectID, orgID,
	).Scan(&projectStatus)
	if err != nil {
		return 0, ErrNotFound
	}
	if projectStatus != "active" {
		return 0, ErrInactive
	}

	var envStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM project_environments
		WHERE id = $1 AND project_id = $2
		FOR UPDATE`, envID, projectID,
	).Scan(&envStatus)
	if err != nil {
		return 0, ErrNotFound
	}
	if envStatus != "active" {
		return 0, ErrInactive
	}

	var rev int64
	_, err = tx.Exec(ctx, `
		INSERT INTO environment_module_pin_state (environment_id, revision, updated_at)
		VALUES ($1, 0, now())
		ON CONFLICT (environment_id) DO NOTHING`, envID)
	if err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx, `
		SELECT revision FROM environment_module_pin_state
		WHERE environment_id = $1 FOR UPDATE`, envID,
	).Scan(&rev)
	if err != nil {
		return 0, err
	}
	if expectedRevision != nil && *expectedRevision != rev {
		return 0, ErrConflictRevision
	}

	before, err := r.listPinsTx(ctx, tx, envID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM environment_module_pins WHERE environment_id = $1`, envID); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, pin := range pins {
		var publishedAt *time.Time
		err = tx.QueryRow(ctx, `
			SELECT published_at
			FROM module_releases
			WHERE id = $1 AND module_slug = $2
			FOR SHARE`, pin.ReleaseID, pin.ModuleSlug,
		).Scan(&publishedAt)
		if err != nil {
			return 0, ErrInvalidInput
		}
		if publishedAt == nil {
			return 0, ErrInvalidInput
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO environment_module_pins (environment_id, module_slug, release_id, pinned_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)`,
			envID, pin.ModuleSlug, pin.ReleaseID, now, now,
		); err != nil {
			return 0, err
		}
	}
	newRev := rev + 1
	if _, err := tx.Exec(ctx, `
		UPDATE environment_module_pin_state
		SET revision = $2, updated_at = now()
		WHERE environment_id = $1`, envID, newRev); err != nil {
		return 0, err
	}

	afterMeta := make([]map[string]string, 0, len(pins))
	for _, pin := range pins {
		afterMeta = append(afterMeta, map[string]string{
			"module_slug": pin.ModuleSlug,
			"version":     pin.Version,
			"release_id":  pin.ReleaseID.String(),
		})
	}
	beforeMeta := make([]map[string]string, 0, len(before))
	for _, pin := range before {
		beforeMeta = append(beforeMeta, map[string]string{
			"module_slug": pin.ModuleSlug,
			"version":     pin.Version,
			"release_id":  pin.ReleaseID.String(),
		})
	}
	meta, _ := json.Marshal(map[string]any{
		"before":   beforeMeta,
		"after":    afterMeta,
		"revision": newRev,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO control_plane_audit_events (
			id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		uuid.Must(uuid.NewV7()), actorUserID, orgID, "environment.modules.pinned", "environment", envID.String(), requestID, meta,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newRev, nil
}
