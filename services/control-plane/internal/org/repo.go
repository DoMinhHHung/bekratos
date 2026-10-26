package org

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrLastOwner    = errors.New("cannot remove or demote the last owner")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateOrganizationWithOwner(ctx context.Context, org Organization, ownerUserID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO organizations (
			id, slug, display_name, status, created_by_identity_user_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		org.ID, org.Slug, org.DisplayName, org.Status, org.CreatedByIdentityUserID, org.CreatedAt, org.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}

	now := org.CreatedAt
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_memberships (
			organization_id, identity_user_id, role, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		org.ID, ownerUserID, RoleOwner, MembershipActive, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) ListOrganizationsForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT o.id, o.slug, o.display_name, o.status, o.created_by_identity_user_id,
		       o.created_at, o.updated_at, o.archived_at
		FROM organizations o
		INNER JOIN organization_memberships m
			ON m.organization_id = o.id
		WHERE m.identity_user_id = $1
		  AND m.status = 'active'
		  AND o.status <> 'archived'
		ORDER BY o.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrgs(rows)
}

func (r *Repository) GetOrganization(ctx context.Context, orgID uuid.UUID) (Organization, error) {
	var o Organization
	var archivedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, status, created_by_identity_user_id,
		       created_at, updated_at, archived_at
		FROM organizations WHERE id = $1`, orgID).Scan(
		&o.ID, &o.Slug, &o.DisplayName, &o.Status, &o.CreatedByIdentityUserID,
		&o.CreatedAt, &o.UpdatedAt, &archivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, err
	}
	o.ArchivedAt = archivedAt
	return o, nil
}

func (r *Repository) GetActiveMembership(ctx context.Context, orgID, userID uuid.UUID) (Membership, error) {
	var m Membership
	err := r.pool.QueryRow(ctx, `
		SELECT organization_id, identity_user_id, role, status, created_at, updated_at
		FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 AND status = 'active'`,
		orgID, userID,
	).Scan(&m.OrganizationID, &m.IdentityUserID, &m.Role, &m.Status, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, err
	}
	return m, nil
}

func (r *Repository) ArchiveOrganization(ctx context.Context, orgID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE organizations
		SET status = 'archived', archived_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'active'`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListMembers(ctx context.Context, orgID uuid.UUID) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT organization_id, identity_user_id, role, status, created_at, updated_at
		FROM organization_memberships
		WHERE organization_id = $1 AND status = 'active'
		ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.OrganizationID, &m.IdentityUserID, &m.Role, &m.Status, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repository) ChangeMemberRole(ctx context.Context, orgID, targetUserID uuid.UUID, newRole Role) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
		return err
	}

	var current Role
	var status MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, targetUserID,
	).Scan(&current, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != MembershipActive {
		return ErrNotFound
	}

	if current == RoleOwner && newRole != RoleOwner {
		var owners int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM organization_memberships
			WHERE organization_id = $1 AND status = 'active' AND role = 'owner'`, orgID,
		).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET role = $3, updated_at = now()
		WHERE organization_id = $1 AND identity_user_id = $2 AND status = 'active'`,
		orgID, targetUserID, newRole,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (r *Repository) RemoveMember(ctx context.Context, orgID, targetUserID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
		return err
	}

	var current Role
	var status MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, targetUserID,
	).Scan(&current, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != MembershipActive {
		return ErrNotFound
	}

	if current == RoleOwner {
		var owners int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM organization_memberships
			WHERE organization_id = $1 AND status = 'active' AND role = 'owner'`, orgID,
		).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET status = 'removed', updated_at = now()
		WHERE organization_id = $1 AND identity_user_id = $2 AND status = 'active'`,
		orgID, targetUserID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (r *Repository) InsertAudit(ctx context.Context, id uuid.UUID, actor *uuid.UUID, orgID *uuid.UUID, eventType, targetType, targetID, requestID string, metadata []byte) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO control_plane_audit_events (
			id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		id, actor, orgID, eventType, targetType, targetID, requestID, metadata,
	)
	return err
}

func scanOrgs(rows pgx.Rows) ([]Organization, error) {
	var out []Organization
	for rows.Next() {
		var o Organization
		var archivedAt *time.Time
		if err := rows.Scan(
			&o.ID, &o.Slug, &o.DisplayName, &o.Status, &o.CreatedByIdentityUserID,
			&o.CreatedAt, &o.UpdatedAt, &archivedAt,
		); err != nil {
			return nil, err
		}
		o.ArchivedAt = archivedAt
		out = append(out, o)
	}
	return out, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func slugNormalize(s string) string {
	return fmt.Sprintf("%s", s)
}
