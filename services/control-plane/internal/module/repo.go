package module

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		FROM modules ORDER BY slug`)
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

func (r *Repository) ListReleases(ctx context.Context, moduleSlug string) ([]Release, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		FROM module_releases
		WHERE ($1 = '' OR module_slug = $1)
		ORDER BY module_slug, published_at DESC`, moduleSlug)
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
	for i := range out {
		deps, err := r.loadDeps(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Dependencies = deps
		conflicts, err := r.loadConflicts(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Conflicts = conflicts
	}
	return out, nil
}

func (r *Repository) GetRelease(ctx context.Context, moduleSlug, version string) (Release, error) {
	var rel Release
	err := r.pool.QueryRow(ctx, `
		SELECT id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		FROM module_releases WHERE module_slug = $1 AND version = $2`,
		moduleSlug, version,
	).Scan(&rel.ID, &rel.ModuleSlug, &rel.Version, &rel.RuntimeCompat,
		&rel.APIContractVersion, &rel.Metadata, &rel.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Release{}, ErrUnknownRelease
	}
	if err != nil {
		return Release{}, err
	}
	deps, err := r.loadDeps(ctx, rel.ID)
	if err != nil {
		return Release{}, err
	}
	rel.Dependencies = deps
	conflicts, err := r.loadConflicts(ctx, rel.ID)
	if err != nil {
		return Release{}, err
	}
	rel.Conflicts = conflicts
	return rel, nil
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

func (r *Repository) ListPins(ctx context.Context, envID uuid.UUID) ([]Pin, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.environment_id, p.module_slug, p.release_id, r.version, p.pinned_at, p.updated_at
		FROM environment_module_pins p
		INNER JOIN module_releases r ON r.id = p.release_id
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

func (r *Repository) ReplacePins(ctx context.Context, envID uuid.UUID, pins []Pin) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM environment_module_pins WHERE environment_id = $1`, envID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, p := range pins {
		if _, err := tx.Exec(ctx, `
			INSERT INTO environment_module_pins (environment_id, module_slug, release_id, pinned_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)`,
			envID, p.ModuleSlug, p.ReleaseID, now, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
