package project

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateProjectWithDevEnv(ctx context.Context, p Project, env Environment, actorUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var orgStatus string
	var role string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1 AND m.identity_user_id = $2 AND m.status = 'active'
		FOR UPDATE OF o, m`, p.OrganizationID, actorUserID,
	).Scan(&orgStatus, &role)
	if err != nil {
		return ErrNotFound
	}
	if orgStatus != "active" {
		return ErrForbidden
	}
	switch role {
	case "owner", "admin":
	default:
		return ErrForbidden
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO projects (
			id, organization_id, slug, display_name, description, status,
			created_by_identity_user_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.OrganizationID, p.Slug, p.DisplayName, nullIfEmpty(p.Description), p.Status,
		p.CreatedByIdentityUserID, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO project_environments (
			id, project_id, slug, kind, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		env.ID, env.ProjectID, env.Slug, env.Kind, env.Status, env.CreatedAt, env.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, p.OrganizationID, "project.created", "project", p.ID.String(), requestID, nil); err != nil {
		return err
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, p.OrganizationID, "environment.created", "environment", env.ID.String(), requestID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListProjectsByOrg(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, slug, display_name, COALESCE(description, ''), status,
		       created_by_identity_user_id, created_at, updated_at, archived_at
		FROM projects
		WHERE organization_id = $1 AND status <> 'archived'
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

func (r *Repository) GetProject(ctx context.Context, orgID, projectID uuid.UUID) (Project, error) {
	var p Project
	var archivedAt *time.Time
	var desc *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, slug, display_name, description, status,
		       created_by_identity_user_id, created_at, updated_at, archived_at
		FROM projects
		WHERE id = $1 AND organization_id = $2`, projectID, orgID,
	).Scan(&p.ID, &p.OrganizationID, &p.Slug, &p.DisplayName, &desc, &p.Status,
		&p.CreatedByIdentityUserID, &p.CreatedAt, &p.UpdatedAt, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	if desc != nil {
		p.Description = *desc
	}
	p.ArchivedAt = archivedAt
	return p, nil
}

func (r *Repository) UpdateProject(ctx context.Context, orgID, projectID, actorUserID uuid.UUID, requestID string, displayName, description *string) (Project, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback(ctx)

	var orgStatus, role string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1 AND m.identity_user_id = $2 AND m.status = 'active'
		FOR UPDATE OF o, m`, orgID, actorUserID,
	).Scan(&orgStatus, &role)
	if err != nil {
		return Project{}, ErrNotFound
	}
	if orgStatus != "active" {
		return Project{}, ErrForbidden
	}
	switch role {
	case "owner", "admin":
	default:
		return Project{}, ErrForbidden
	}

	tag, err := tx.Exec(ctx, `
		UPDATE projects
		SET display_name = COALESCE($3, display_name),
		    description = COALESCE($4, description),
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'active'`,
		projectID, orgID, displayName, description,
	)
	if err != nil {
		return Project{}, err
	}
	if tag.RowsAffected() == 0 {
		return Project{}, ErrNotFound
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, orgID, "project.updated", "project", projectID.String(), requestID, nil); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Project{}, err
	}
	return r.GetProject(ctx, orgID, projectID)
}

func (r *Repository) ArchiveProject(ctx context.Context, orgID, projectID, actorUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orgStatus, role string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1 AND m.identity_user_id = $2 AND m.status = 'active'
		FOR UPDATE OF o, m`, orgID, actorUserID,
	).Scan(&orgStatus, &role)
	if err != nil {
		return ErrNotFound
	}
	if orgStatus != "active" {
		return ErrForbidden
	}
	switch role {
	case "owner", "admin":
	default:
		return ErrForbidden
	}
	tag, err := tx.Exec(ctx, `
		UPDATE projects
		SET status = 'archived', archived_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'active'`,
		projectID, orgID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, orgID, "project.archived", "project", projectID.String(), requestID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateEnvironment(ctx context.Context, orgID uuid.UUID, env Environment, actorUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orgStatus, role, projectStatus string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1 AND m.identity_user_id = $2 AND m.status = 'active'
		FOR UPDATE OF o, m`, orgID, actorUserID,
	).Scan(&orgStatus, &role)
	if err != nil {
		return ErrNotFound
	}
	if orgStatus != "active" {
		return ErrForbidden
	}
	switch role {
	case "owner", "admin", "developer":
	default:
		return ErrForbidden
	}
	err = tx.QueryRow(ctx, `
		SELECT status FROM projects WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		env.ProjectID, orgID,
	).Scan(&projectStatus)
	if err != nil {
		return ErrNotFound
	}
	if projectStatus != "active" {
		return ErrForbidden
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO project_environments (
			id, project_id, slug, kind, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		env.ID, env.ProjectID, env.Slug, env.Kind, env.Status, env.CreatedAt, env.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, orgID, "environment.created", "environment", env.ID.String(), requestID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]Environment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, project_id, slug, kind, status,
		       active_configuration_version_id, active_deployment_id,
		       created_at, updated_at, archived_at
		FROM project_environments
		WHERE project_id = $1 AND status <> 'archived'
		ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEnvs(rows)
}

func (r *Repository) GetEnvironment(ctx context.Context, projectID, envID uuid.UUID) (Environment, error) {
	var e Environment
	var archivedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, project_id, slug, kind, status,
		       active_configuration_version_id, active_deployment_id,
		       created_at, updated_at, archived_at
		FROM project_environments
		WHERE id = $1 AND project_id = $2`, envID, projectID,
	).Scan(&e.ID, &e.ProjectID, &e.Slug, &e.Kind, &e.Status,
		&e.ActiveConfigurationVersionID, &e.ActiveDeploymentID,
		&e.CreatedAt, &e.UpdatedAt, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, err
	}
	e.ArchivedAt = archivedAt
	return e, nil
}

func (r *Repository) ArchiveEnvironment(ctx context.Context, orgID, projectID, envID, actorUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orgStatus, role, projectStatus string
	err = tx.QueryRow(ctx, `
		SELECT o.status, m.role
		FROM organizations o
		INNER JOIN organization_memberships m ON m.organization_id = o.id
		WHERE o.id = $1 AND m.identity_user_id = $2 AND m.status = 'active'
		FOR UPDATE OF o, m`, orgID, actorUserID,
	).Scan(&orgStatus, &role)
	if err != nil {
		return ErrNotFound
	}
	if orgStatus != "active" {
		return ErrForbidden
	}
	switch role {
	case "owner", "admin":
	default:
		return ErrForbidden
	}
	err = tx.QueryRow(ctx, `
		SELECT status FROM projects WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		projectID, orgID,
	).Scan(&projectStatus)
	if err != nil {
		return ErrNotFound
	}
	tag, err := tx.Exec(ctx, `
		UPDATE project_environments
		SET status = 'archived', archived_at = now(), updated_at = now()
		WHERE id = $1 AND project_id = $2 AND status = 'active'`,
		envID, projectID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := insertProjectAudit(ctx, tx, actorUserID, orgID, "environment.archived", "environment", envID.String(), requestID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanProjects(rows pgx.Rows) ([]Project, error) {
	var out []Project
	for rows.Next() {
		var p Project
		var archivedAt *time.Time
		if err := rows.Scan(
			&p.ID, &p.OrganizationID, &p.Slug, &p.DisplayName, &p.Description, &p.Status,
			&p.CreatedByIdentityUserID, &p.CreatedAt, &p.UpdatedAt, &archivedAt,
		); err != nil {
			return nil, err
		}
		p.ArchivedAt = archivedAt
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanEnvs(rows pgx.Rows) ([]Environment, error) {
	var out []Environment
	for rows.Next() {
		var e Environment
		var archivedAt *time.Time
		if err := rows.Scan(
			&e.ID, &e.ProjectID, &e.Slug, &e.Kind, &e.Status,
			&e.ActiveConfigurationVersionID, &e.ActiveDeploymentID,
			&e.CreatedAt, &e.UpdatedAt, &archivedAt,
		); err != nil {
			return nil, err
		}
		e.ArchivedAt = archivedAt
		out = append(out, e)
	}
	return out, rows.Err()
}

func insertProjectAudit(ctx context.Context, tx pgx.Tx, actor, orgID uuid.UUID, eventType, targetType, targetID, requestID string, metadata []byte) error {
	if metadata == nil {
		metadata = []byte("{}")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO control_plane_audit_events (
			id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		uuid.Must(uuid.NewV7()), actor, orgID, eventType, targetType, targetID, requestID, metadata,
	)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
