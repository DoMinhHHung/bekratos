package module

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrConflictRevision = errors.New("revision conflict")

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

func (r *Repository) ListPins(ctx context.Context, envID uuid.UUID) ([]Pin, int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.environment_id, p.module_slug, p.release_id, r.version, p.pinned_at, p.updated_at
		FROM environment_module_pins p
		INNER JOIN module_releases r ON r.id = p.release_id AND r.module_slug = p.module_slug
		WHERE p.environment_id = $1
		ORDER BY p.module_slug`, envID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Pin
	for rows.Next() {
		var p Pin
		if err := rows.Scan(&p.EnvironmentID, &p.ModuleSlug, &p.ReleaseID, &p.Version, &p.PinnedAt, &p.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var rev int64
	err = r.pool.QueryRow(ctx, `
		SELECT revision FROM environment_module_pin_state WHERE environment_id = $1`, envID,
	).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return out, rev, nil
}

func (r *Repository) ReplacePins(ctx context.Context, envID uuid.UUID, pins []Pin, expectedRevision *int64, actorUserID *uuid.UUID, orgID *uuid.UUID) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var rev int64
	err = tx.QueryRow(ctx, `
		SELECT revision FROM environment_module_pin_state
		WHERE environment_id = $1 FOR UPDATE`, envID,
	).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO environment_module_pin_state (environment_id, revision, updated_at)
			VALUES ($1, 0, now())`, envID); err != nil {
			return 0, err
		}
		rev = 0
		if err := tx.QueryRow(ctx, `
			SELECT revision FROM environment_module_pin_state
			WHERE environment_id = $1 FOR UPDATE`, envID,
		).Scan(&rev); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if expectedRevision != nil && *expectedRevision != rev {
		return 0, ErrConflictRevision
	}

	before, _ := r.listPinsTx(ctx, tx, envID)

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
		uuid.Must(uuid.NewV7()), actorUserID, orgID, "environment.modules.pinned", "environment", envID.String(), "", meta,
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
