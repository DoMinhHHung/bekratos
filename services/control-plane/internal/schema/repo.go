package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type EnvironmentChain struct {
	OrganizationID uuid.UUID
	OrgStatus      string
	ProjectID      uuid.UUID
	ProjectStatus  string
	EnvironmentID  uuid.UUID
	EnvStatus      string
}

type MutationAuth struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	ActorID   uuid.UUID
	Role      org.Role
}

func (r *Repository) GetEnvironmentChain(ctx context.Context, orgID, projectID, envID uuid.UUID) (EnvironmentChain, error) {
	const q = `
SELECT o.id, o.status, p.id, p.status, e.id, e.status
FROM organizations o
JOIN projects p ON p.organization_id = o.id
JOIN project_environments e ON e.project_id = p.id
WHERE o.id = $1 AND p.id = $2 AND e.id = $3`
	var c EnvironmentChain
	err := r.pool.QueryRow(ctx, q, orgID, projectID, envID).Scan(
		&c.OrganizationID, &c.OrgStatus,
		&c.ProjectID, &c.ProjectStatus,
		&c.EnvironmentID, &c.EnvStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentChain{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentChain{}, err
	}
	return c, nil
}

func (r *Repository) GetDraft(ctx context.Context, envID uuid.UUID) (Draft, error) {
	const q = `
SELECT environment_id, revision, schema_json, created_at, updated_at
FROM environment_schema_drafts
WHERE environment_id = $1`
	var d Draft
	var raw []byte
	err := r.pool.QueryRow(ctx, q, envID).Scan(
		&d.EnvironmentID, &d.Revision, &raw, &d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{
			EnvironmentID: envID,
			Revision:      0,
			Document:      EmptyDocument(),
		}, nil
	}
	if err != nil {
		return Draft{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		d.Document = EmptyDocument()
	} else {
		if err := json.Unmarshal(raw, &d.Document); err != nil {
			return Draft{}, fmt.Errorf("decode schema_json: %w", err)
		}
		if d.Document.Entities == nil {
			d.Document.Entities = map[string]EntityDefinition{}
		}
	}
	return d, nil
}

func (r *Repository) SaveDraft(
	ctx context.Context,
	auth MutationAuth,
	expectedRevision int64,
	doc SchemaDocument,
	requestID string,
	action string,
) (Draft, error) {
	if expectedRevision < 0 {
		return Draft{}, ErrInvalidInput
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return Draft{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback(ctx)

	var role string
	var orgStatus, projectStatus, envStatus string
	err = tx.QueryRow(ctx, `
SELECT m.role, o.status, p.status, e.status
FROM organizations o
JOIN organization_memberships m
  ON m.organization_id = o.id AND m.identity_user_id = $4 AND m.status = 'active'
JOIN projects p ON p.organization_id = o.id AND p.id = $2
JOIN project_environments e ON e.project_id = p.id AND e.id = $3
WHERE o.id = $1
FOR UPDATE OF o, p, e`,
		auth.OrgID, auth.ProjectID, auth.EnvID, auth.ActorID,
	).Scan(&role, &orgStatus, &projectStatus, &envStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	parsedRole, ok := org.ParseRole(role)
	if !ok {
		return Draft{}, ErrForbidden
	}
	if !parsedRole.CanMutateSchema() {
		return Draft{}, ErrForbidden
	}
	if orgStatus != string(org.OrgStatusActive) || projectStatus != "active" || envStatus != "active" {
		return Draft{}, ErrInactive
	}

	var currentRevision int64
	var exists bool
	err = tx.QueryRow(ctx, `
SELECT revision, true FROM environment_schema_drafts WHERE environment_id = $1 FOR UPDATE`,
		auth.EnvID,
	).Scan(&currentRevision, &exists)
	if errors.Is(err, pgx.ErrNoRows) {
		currentRevision = 0
		exists = false
	} else if err != nil {
		return Draft{}, err
	}

	if currentRevision != expectedRevision {
		return Draft{}, ErrConflictRevision
	}

	now := time.Now().UTC()
	newRevision := currentRevision + 1
	auditID := uuid.Must(uuid.NewV7())
	targetID := auth.EnvID.String()
	metadata, _ := json.Marshal(map[string]any{
		"environment_id":    targetID,
		"previous_revision": expectedRevision,
		"new_revision":      newRevision,
		"action":            action,
	})

	if !exists {
		_, err = tx.Exec(ctx, `
INSERT INTO environment_schema_drafts (environment_id, revision, schema_json, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)`,
			auth.EnvID, newRevision, raw, now,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return Draft{}, ErrConflictRevision
			}
			return Draft{}, err
		}
	} else {
		ct, execErr := tx.Exec(ctx, `
UPDATE environment_schema_drafts
SET revision = $2, schema_json = $3, updated_at = $4
WHERE environment_id = $1 AND revision = $5`,
			auth.EnvID, newRevision, raw, now, expectedRevision,
		)
		if execErr != nil {
			return Draft{}, execErr
		}
		if ct.RowsAffected() != 1 {
			return Draft{}, ErrConflictRevision
		}
	}

	_, err = tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		auditID,
		auth.ActorID,
		auth.OrgID,
		action,
		"environment_schema_draft",
		targetID,
		requestID,
		metadata,
		now,
	)
	if err != nil {
		return Draft{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Draft{}, err
	}

	return Draft{
		EnvironmentID: auth.EnvID,
		Revision:      newRevision,
		Document:      doc,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}
