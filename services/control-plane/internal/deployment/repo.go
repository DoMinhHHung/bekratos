package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
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

type MutationAuth struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	ActorID   uuid.UUID
}

func (r *Repository) lockAuthChain(ctx context.Context, tx pgx.Tx, auth MutationAuth) (org.Role, error) {
	var role, orgStatus, projectStatus, envStatus string
	err := tx.QueryRow(ctx, `
SELECT m.role, o.status, p.status, e.status
FROM organizations o
JOIN organization_memberships m
  ON m.organization_id = o.id AND m.identity_user_id = $4 AND m.status = 'active'
JOIN projects p ON p.organization_id = o.id AND p.id = $2
JOIN project_environments e ON e.project_id = p.id AND e.id = $3
WHERE o.id = $1
FOR UPDATE OF m, o, p, e`,
		auth.OrgID, auth.ProjectID, auth.EnvID, auth.ActorID,
	).Scan(&role, &orgStatus, &projectStatus, &envStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if orgStatus != "active" || projectStatus != "active" || envStatus != "active" {
		return "", ErrInactive
	}
	parsed, ok := org.ParseRole(role)
	if !ok {
		return "", ErrForbidden
	}
	return parsed, nil
}

func (r *Repository) Create(ctx context.Context, auth MutationAuth, in CreateInput, requestID string) (CreateResult, error) {
	if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 128 {
		return CreateResult{}, fmt.Errorf("%w: idempotency_key", ErrInvalidInput)
	}
	mode := in.TargetMode
	if mode == "" {
		mode = TargetModeShared
	}
	if mode != TargetModeShared {
		return CreateResult{}, fmt.Errorf("%w: target_mode", ErrInvalidInput)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback(ctx)

	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return CreateResult{}, err
	}
	if !role.CanDeploy() {
		return CreateResult{}, ErrForbidden
	}

	if existing, ok, err := r.findIdempotent(ctx, tx, auth.EnvID, in.IdempotencyKey); err != nil {
		return CreateResult{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return CreateResult{}, err
		}
		return CreateResult{Deployment: existing, Reused: true}, nil
	}

	versionID, err := r.resolveVersionID(ctx, tx, auth.EnvID, in.ConfigurationVersionID)
	if err != nil {
		return CreateResult{}, err
	}

	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	providerReq := "dep-" + id.String()
	_, err = tx.Exec(ctx, `
INSERT INTO deployments (
  id, environment_id, configuration_version_id, target_mode, provider, status,
  attempt_count, max_attempts, idempotency_key, provider_request_id,
  next_attempt_at, created_by_identity_user_id, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,0,5,$7,$8,$9,$10,$11,$12)`,
		id, auth.EnvID, versionID, string(mode), string(ProviderUnavailable), string(StatusQueued),
		in.IdempotencyKey, providerReq, now, auth.ActorID, now, now,
	)
	if err != nil {
		return CreateResult{}, err
	}

	meta, _ := json.Marshal(map[string]any{
		"deployment_id":            id.String(),
		"configuration_version_id": versionID.String(),
		"target_mode":              string(mode),
		"provider":                 string(ProviderUnavailable),
	})
	if err := r.insertAudit(ctx, tx, auth, "deployment.create", id.String(), requestID, meta, now); err != nil {
		return CreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}

	d := Deployment{
		ID: id, EnvironmentID: auth.EnvID, ConfigurationVersionID: versionID,
		TargetMode: mode, Provider: ProviderUnavailable, Status: StatusQueued,
		MaxAttempts: 5, IdempotencyKey: in.IdempotencyKey, ProviderRequestID: providerReq,
		NextAttemptAt: now, CreatedByUserID: auth.ActorID, CreatedAt: now, UpdatedAt: now,
	}
	return CreateResult{Deployment: d, Reused: false}, nil
}

func (r *Repository) resolveVersionID(ctx context.Context, tx pgx.Tx, envID uuid.UUID, explicit *uuid.UUID) (uuid.UUID, error) {
	if explicit != nil {
		var exists bool
		err := tx.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM environment_configuration_versions
  WHERE id = $1 AND environment_id = $2
)`, *explicit, envID).Scan(&exists)
		if err != nil {
			return uuid.Nil, err
		}
		if !exists {
			return uuid.Nil, ErrConfigMismatch
		}
		return *explicit, nil
	}
	var active *uuid.UUID
	err := tx.QueryRow(ctx, `
SELECT active_configuration_version_id
FROM project_environments
WHERE id = $1
FOR UPDATE`, envID).Scan(&active)
	if err != nil {
		return uuid.Nil, err
	}
	if active == nil {
		return uuid.Nil, ErrNoActiveConfig
	}
	return *active, nil
}

func (r *Repository) findIdempotent(ctx context.Context, tx pgx.Tx, envID uuid.UUID, key string) (Deployment, bool, error) {
	d, err := scanDeployment(tx.QueryRow(ctx, deploymentSelect+`
WHERE environment_id = $1 AND idempotency_key = $2`, envID, key))
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	return d, true, nil
}

func (r *Repository) List(ctx context.Context, auth MutationAuth) ([]Deployment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return nil, err
	}
	if !role.CanReadDeployments() {
		return nil, ErrForbidden
	}
	rows, err := tx.Query(ctx, deploymentSelect+`
WHERE environment_id = $1
ORDER BY created_at DESC
LIMIT 100`, auth.EnvID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) Get(ctx context.Context, auth MutationAuth, deploymentID uuid.UUID) (Deployment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return Deployment{}, err
	}
	if !role.CanReadDeployments() {
		return Deployment{}, ErrForbidden
	}
	d, err := scanDeployment(tx.QueryRow(ctx, deploymentSelect+`
WHERE id = $1 AND environment_id = $2`, deploymentID, auth.EnvID))
	if err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

func (r *Repository) Claim(ctx context.Context, workerID string, lease time.Duration, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 5
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	leaseUntil := now.Add(lease)
	rows, err := tx.Query(ctx, `
WITH candidates AS (
  SELECT id
  FROM deployments
  WHERE status IN ('queued', 'provisioning')
    AND next_attempt_at <= $1
    AND (lease_expires_at IS NULL OR lease_expires_at < $1)
  ORDER BY next_attempt_at
  FOR UPDATE SKIP LOCKED
  LIMIT $2
)
UPDATE deployments d
SET status = 'provisioning',
    attempt_count = d.attempt_count + 1,
    lease_owner = $3,
    lease_expires_at = $4,
    fencing_token = d.fencing_token + 1,
    started_at = COALESCE(d.started_at, $1),
    updated_at = $1
FROM candidates c
WHERE d.id = c.id
RETURNING `+deploymentColumns, now, limit, workerID, leaseUntil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) MarkFailed(ctx context.Context, d Deployment, workerID string, code, message string, terminal bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	status := StatusFailed
	var finished *time.Time
	next := now.Add(backoff(d.AttemptCount))
	if terminal || d.AttemptCount >= d.MaxAttempts {
		finished = &now
		next = now
	} else {
		status = StatusQueued
	}
	ct, err := tx.Exec(ctx, `
UPDATE deployments
SET status = $2,
    failure_code = $3,
    failure_message = $4,
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = $5,
    finished_at = $6,
    updated_at = $7
WHERE id = $1
  AND fencing_token = $8
  AND lease_owner = $9`,
		d.ID, string(status), code, sanitizeMessage(message), next, finished, now, d.FencingToken, workerID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	return tx.Commit(ctx)
}

func (r *Repository) MarkReady(ctx context.Context, d Deployment, workerID string, res ProvisionResult) error {
	if res.Outcome != OutcomeSuccess {
		return ErrReadyWithoutEvidence
	}
	if res.EnvironmentID != d.EnvironmentID || res.VersionID != d.ConfigurationVersionID {
		return ErrReadyWithoutEvidence
	}
	if res.Host == "" && res.URL == "" {
		return ErrReadyWithoutEvidence
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	var url, host, region *string
	if res.URL != "" {
		url = &res.URL
	}
	if res.Host != "" {
		host = &res.Host
	}
	if res.Region != "" {
		region = &res.Region
	}
	ct, err := tx.Exec(ctx, `
UPDATE deployments
SET status = 'ready',
    failure_code = '',
    failure_message = '',
    provider_url = $2,
    provider_host = $3,
    provider_region = $4,
    lease_owner = NULL,
    lease_expires_at = NULL,
    ready_at = $5,
    finished_at = $5,
    updated_at = $5
WHERE id = $1
  AND fencing_token = $6
  AND lease_owner = $7
  AND status = 'provisioning'`,
		d.ID, url, host, region, now, d.FencingToken, workerID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	return tx.Commit(ctx)
}

func (r *Repository) insertAudit(ctx context.Context, tx pgx.Tx, auth MutationAuth, eventType, targetID, requestID string, meta []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		uuid.Must(uuid.NewV7()), auth.ActorID, auth.OrgID, eventType, "deployment", targetID, requestID, meta, now,
	)
	return err
}

const deploymentColumns = `
id, environment_id, configuration_version_id, target_mode, provider, status,
failure_code, failure_message, attempt_count, max_attempts, idempotency_key, provider_request_id,
lease_owner, lease_expires_at, fencing_token, next_attempt_at,
provider_url, provider_host, provider_region,
created_by_identity_user_id, created_at, updated_at, started_at, ready_at, finished_at`

const deploymentSelect = `SELECT ` + deploymentColumns + ` FROM deployments `

func scanDeployment(row interface {
	Scan(dest ...any) error
}) (Deployment, error) {
	var d Deployment
	var mode, provider, status string
	err := row.Scan(
		&d.ID, &d.EnvironmentID, &d.ConfigurationVersionID, &mode, &provider, &status,
		&d.FailureCode, &d.FailureMessage, &d.AttemptCount, &d.MaxAttempts, &d.IdempotencyKey, &d.ProviderRequestID,
		&d.LeaseOwner, &d.LeaseExpiresAt, &d.FencingToken, &d.NextAttemptAt,
		&d.ProviderURL, &d.ProviderHost, &d.ProviderRegion,
		&d.CreatedByUserID, &d.CreatedAt, &d.UpdatedAt, &d.StartedAt, &d.ReadyAt, &d.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, err
	}
	d.TargetMode = TargetMode(mode)
	d.Provider = ProviderName(provider)
	d.Status = Status(status)
	return d, nil
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(attempt*attempt) * time.Second
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

func sanitizeMessage(s string) string {
	if len(s) > 512 {
		return s[:512]
	}
	return s
}
