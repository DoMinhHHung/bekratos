package org

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CreateOrganizationWithOwner(ctx context.Context, org Organization, ownerUserID, actorUserID uuid.UUID, requestID string, auditMeta []byte) error {
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

	if err := insertAuditTx(ctx, tx, uuid.Must(uuid.NewV7()), &actorUserID, &org.ID, "organization.created", "organization", org.ID.String(), requestID, auditMeta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ArchiveOrganizationAtomic(ctx context.Context, orgID, actorUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
		return err
	}

	var role Role
	var status MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, actorUserID,
	).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != MembershipActive {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !role.CanArchiveOrg() {
		return ErrForbidden
	}

	tag, err := tx.Exec(ctx, `
		UPDATE organizations
		SET status = 'archived', archived_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'active'`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if err := insertAuditTx(ctx, tx, uuid.Must(uuid.NewV7()), &actorUserID, &orgID, "organization.archived", "organization", orgID.String(), requestID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ChangeMemberRoleAtomic(ctx context.Context, orgID, actorID, targetUserID uuid.UUID, newRole Role, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var orgStatus OrgStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&orgStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if orgStatus != OrgStatusActive {
		return ErrNotFound
	}

	var actorRole Role
	var actorStatus MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, actorID,
	).Scan(&actorRole, &actorStatus)
	if errors.Is(err, pgx.ErrNoRows) || actorStatus != MembershipActive {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !actorRole.CanManageMembers() || !actorRole.CanAssignRole(newRole) {
		return ErrForbidden
	}

	var current Role
	var status MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, targetUserID,
	).Scan(&current, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != MembershipActive {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !actorRole.CanModifyMember(current) {
		return ErrForbidden
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

	meta, _ := json.Marshal(map[string]any{"from": string(current), "to": string(newRole)})
	if err := insertAuditTx(ctx, tx, uuid.Must(uuid.NewV7()), &actorID, &orgID, "membership.role_changed", "membership", targetUserID.String(), requestID, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) RemoveMemberAtomic(ctx context.Context, orgID, actorID, targetUserID uuid.UUID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var orgStatus OrgStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&orgStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if orgStatus != OrgStatusActive {
		return ErrNotFound
	}

	var actorRole Role
	var actorStatus MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, actorID,
	).Scan(&actorRole, &actorStatus)
	if errors.Is(err, pgx.ErrNoRows) || actorStatus != MembershipActive {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !actorRole.CanManageMembers() {
		return ErrForbidden
	}

	var current Role
	var status MembershipStatus
	err = tx.QueryRow(ctx, `
		SELECT role, status FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`,
		orgID, targetUserID,
	).Scan(&current, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != MembershipActive {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !actorRole.CanModifyMember(current) {
		return ErrForbidden
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

	meta, _ := json.Marshal(map[string]any{"role": string(current)})
	if err := insertAuditTx(ctx, tx, uuid.Must(uuid.NewV7()), &actorID, &orgID, "membership.removed", "membership", targetUserID.String(), requestID, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertAuditTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor, orgID *uuid.UUID, eventType, targetType, targetID, requestID string, metadata []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO control_plane_audit_events (
			id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
		id, actor, orgID, eventType, targetType, targetID, requestID, metadata,
	)
	return err
}
