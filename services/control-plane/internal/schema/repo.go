package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type EnvironmentChain struct {
	OrganizationID uuid.UUID
	OrgStatus      string
	ProjectID      uuid.UUID
	ProjectStatus  string
	EnvironmentID  uuid.UUID
	EnvStatus      string
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
	envID uuid.UUID,
	expectedRevision int64,
	doc SchemaDocument,
	actorID uuid.UUID,
	requestID string,
	action string,
) (Draft, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return Draft{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback(ctx)

	var currentRevision int64
	var exists bool
	err = tx.QueryRow(ctx, `
SELECT revision, true FROM environment_schema_drafts WHERE environment_id = $1 FOR UPDATE`,
		envID,
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

	if !exists {
		_, err = tx.Exec(ctx, `
INSERT INTO environment_schema_drafts (environment_id, revision, schema_json, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)`,
			envID, newRevision, raw, now,
		)
	} else {
		ct, execErr := tx.Exec(ctx, `
UPDATE environment_schema_drafts
SET revision = $2, schema_json = $3, updated_at = $4
WHERE environment_id = $1 AND revision = $5`,
			envID, newRevision, raw, now, expectedRevision,
		)
		if execErr != nil {
			return Draft{}, execErr
		}
		if ct.RowsAffected() != 1 {
			return Draft{}, ErrConflictRevision
		}
	}
	if err != nil {
		return Draft{}, err
	}

	metadata, _ := json.Marshal(map[string]any{
		"environment_id":    envID.String(),
		"previous_revision": expectedRevision,
		"new_revision":      newRevision,
		"action":            action,
	})
	_, err = tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
)
SELECT $1, $2, p.organization_id, $3, 'environment_schema_draft', $4, $5, $6, $7
FROM project_environments e
JOIN projects p ON p.id = e.project_id
WHERE e.id = $4`,
		uuid.Must(uuid.NewV7()), actorID, action, envID.String(), requestID, metadata, now,
	)
	if err != nil {
		return Draft{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Draft{}, err
	}

	return Draft{
		EnvironmentID: envID,
		Revision:      newRevision,
		Document:      doc,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}
