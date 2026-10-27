package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
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

func requestFingerprint(mode TargetMode, versionChoice string) string {
	payload := "v1|mode=" + string(mode) + "|version=" + versionChoice
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func (r *Repository) Create(ctx context.Context, auth MutationAuth, in CreateInput, requestID string, provider ProviderName) (CreateResult, error) {
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 128 {
		return CreateResult{}, fmt.Errorf("%w: idempotency_key", ErrInvalidInput)
	}
	if provider == "" {
		provider = ProviderUnavailable
	}
	mode := in.TargetMode
	if mode == "" {
		mode = TargetModeShared
	}
	if mode != TargetModeShared {
		return CreateResult{}, fmt.Errorf("%w: target_mode", ErrInvalidInput)
	}

	versionChoice := "active"
	if in.ConfigurationVersionID != nil {
		versionChoice = in.ConfigurationVersionID.String()
	}
	fp := requestFingerprint(mode, versionChoice)

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
		if existing.RequestFingerprint != fp {
			return CreateResult{}, ErrConflict
		}
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
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO deployments (
  id, environment_id, configuration_version_id, target_mode, provider, status,
  attempt_count, max_attempts, idempotency_key, request_fingerprint, provider_request_id,
  next_attempt_at, created_by_identity_user_id, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,0,5,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (environment_id, idempotency_key) DO NOTHING
RETURNING id`,
		id, auth.EnvID, versionID, string(mode), string(provider), string(StatusQueued),
		in.IdempotencyKey, fp, providerReq, now, auth.ActorID, now, now,
	).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, ok, findErr := r.findIdempotent(ctx, tx, auth.EnvID, in.IdempotencyKey)
		if findErr != nil {
			return CreateResult{}, findErr
		}
		if !ok || existing.RequestFingerprint != fp {
			return CreateResult{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateResult{}, err
		}
		return CreateResult{Deployment: existing, Reused: true}, nil
	}
	if err != nil {
		return CreateResult{}, err
	}
	if insertedID != id {
		return CreateResult{}, ErrConflict
	}

	meta, _ := json.Marshal(map[string]any{
		"deployment_id":            id.String(),
		"configuration_version_id": versionID.String(),
		"target_mode":              string(mode),
		"provider":                 string(provider),
		"provider_request_id":      providerReq,
	})
	if err := r.insertAudit(ctx, tx, auth, "deployment.create", id.String(), requestID, meta, now); err != nil {
		return CreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}

	d := Deployment{
		ID: id, EnvironmentID: auth.EnvID, ConfigurationVersionID: versionID,
		TargetMode: mode, Provider: provider, Status: StatusQueued,
		MaxAttempts: 5, IdempotencyKey: in.IdempotencyKey, RequestFingerprint: fp,
		ProviderRequestID: providerReq,
		NextAttemptAt:     now, CreatedByUserID: auth.ActorID, CreatedAt: now, UpdatedAt: now,
	}
	return CreateResult{Deployment: d, Reused: false}, nil
}

func (r *Repository) resolveVersionID(ctx context.Context, tx pgx.Tx, envID uuid.UUID, explicit *uuid.UUID) (uuid.UUID, error) {
	if explicit != nil {
		var state string
		err := tx.QueryRow(ctx, `
SELECT state
FROM environment_configuration_versions
WHERE id = $1 AND environment_id = $2
FOR UPDATE`, *explicit, envID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrConfigMismatch
		}
		if err != nil {
			return uuid.Nil, err
		}
		if state != "active" && state != "superseded" {
			return uuid.Nil, ErrVersionNotDeployable
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
	var belongs bool
	err = tx.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM environment_configuration_versions
  WHERE id = $1 AND environment_id = $2
)`, *active, envID).Scan(&belongs)
	if err != nil {
		return uuid.Nil, err
	}
	if !belongs {
		return uuid.Nil, ErrConfigMismatch
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

	terminalRows, err := tx.Query(ctx, `
UPDATE deployments
SET status = 'failed',
    failure_code = 'max_attempts_exceeded',
    failure_message = 'deployment exceeded maximum provider attempts',
    lease_owner = NULL,
    lease_expires_at = NULL,
    finished_at = $1,
    updated_at = $1,
    next_attempt_at = $1
WHERE status IN ('queued', 'provisioning')
  AND attempt_count >= max_attempts
  AND (lease_expires_at IS NULL OR lease_expires_at < $1)
RETURNING id, environment_id, configuration_version_id, provider_request_id, attempt_count, max_attempts`,
		now,
	)
	if err != nil {
		return nil, err
	}
	var exhausted []Deployment
	for terminalRows.Next() {
		var d Deployment
		if err := terminalRows.Scan(&d.ID, &d.EnvironmentID, &d.ConfigurationVersionID, &d.ProviderRequestID, &d.AttemptCount, &d.MaxAttempts); err != nil {
			terminalRows.Close()
			return nil, err
		}
		exhausted = append(exhausted, d)
	}
	terminalRows.Close()
	if err := terminalRows.Err(); err != nil {
		return nil, err
	}
	for _, d := range exhausted {
		metadata, _ := json.Marshal(map[string]any{
			"status":              string(StatusFailed),
			"failure_code":        "max_attempts_exceeded",
			"attempt_count":       d.AttemptCount,
			"max_attempts":        d.MaxAttempts,
			"provider_request_id": d.ProviderRequestID,
		})
		if err := r.insertOutcomeAudit(ctx, tx, d, "deployment.failed", metadata, now); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(ctx, `
WITH candidates AS (
  SELECT d.id AS deployment_id
  FROM deployments d
  WHERE d.status IN ('queued', 'provisioning')
    AND d.next_attempt_at <= $1
    AND d.attempt_count < d.max_attempts
    AND (d.lease_expires_at IS NULL OR d.lease_expires_at < $1)
  ORDER BY d.next_attempt_at
  FOR UPDATE OF d SKIP LOCKED
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
WHERE d.id = c.deployment_id
RETURNING
  d.id, d.environment_id, d.configuration_version_id, d.target_mode, d.provider, d.status,
  d.failure_code, d.failure_message, d.attempt_count, d.max_attempts, d.idempotency_key,
  d.request_fingerprint, d.provider_request_id,
  d.lease_owner, d.lease_expires_at, d.fencing_token, d.next_attempt_at,
  d.provider_url, d.provider_host, d.provider_region, d.readiness_probe_id, d.readiness_observed_at,
  d.created_by_identity_user_id, d.created_at, d.updated_at, d.started_at, d.ready_at, d.finished_at`,
		now, limit, workerID, leaseUntil)
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
	code, message = safePublicFailure(code)
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
		status = StatusFailed
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
  AND lease_owner = $9
  AND status = 'provisioning'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at > clock_timestamp()`,
		d.ID, string(status), code, message, next, finished, now, d.FencingToken, workerID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	eventType := "deployment.failed"
	if status == StatusQueued {
		eventType = "deployment.retry_scheduled"
	}
	metadata, _ := json.Marshal(map[string]any{
		"status":              string(status),
		"failure_code":        code,
		"attempt_count":       d.AttemptCount,
		"max_attempts":        d.MaxAttempts,
		"provider_request_id": d.ProviderRequestID,
	})
	if err := r.insertOutcomeAudit(ctx, tx, d, eventType, metadata, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func normalizeProviderHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@?# \t\r\n") {
		return "", ErrReadyWithoutEvidence
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", ErrReadyWithoutEvidence
		}
		return ip.String(), nil
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", ErrReadyWithoutEvidence
	}
	hasAlphabeticTLD := false
	for _, ch := range labels[len(labels)-1] {
		if ch >= 'a' && ch <= 'z' {
			hasAlphabeticTLD = true
			break
		}
	}
	if !hasAlphabeticTLD {
		return "", ErrReadyWithoutEvidence
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrReadyWithoutEvidence
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return "", ErrReadyWithoutEvidence
			}
		}
	}
	return host, nil
}

func validateProviderTarget(res ProvisionResult) (*string, *string, error) {
	rawURL := strings.TrimSpace(res.URL)
	rawHost := strings.TrimSpace(res.Host)
	var urlValue *string
	var targetHost string

	if rawURL != "" {
		if len(rawURL) > 2048 {
			return nil, nil, ErrReadyWithoutEvidence
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(rawURL, "#") {
			return nil, nil, ErrReadyWithoutEvidence
		}
		targetHost, err = normalizeProviderHost(parsed.Hostname())
		if err != nil {
			return nil, nil, err
		}
		urlValue = &rawURL
	}

	if rawHost != "" {
		normalizedHost, err := normalizeProviderHost(rawHost)
		if err != nil {
			return nil, nil, err
		}
		if targetHost != "" && normalizedHost != targetHost {
			return nil, nil, ErrReadyWithoutEvidence
		}
		targetHost = normalizedHost
	}

	if targetHost == "" {
		return nil, nil, ErrReadyWithoutEvidence
	}
	hostValue := targetHost
	return urlValue, &hostValue, nil
}

func (r *Repository) MarkReady(ctx context.Context, d Deployment, workerID string, res ProvisionResult) error {
	if res.Outcome != OutcomeSuccess || res.DeploymentID != d.ID {
		return ErrReadyWithoutEvidence
	}
	if res.EnvironmentID != d.EnvironmentID || res.VersionID != d.ConfigurationVersionID {
		return ErrReadyWithoutEvidence
	}
	if res.RequestID != d.ProviderRequestID || !res.ReadinessProbePassed || strings.TrimSpace(res.ReadinessProbeID) == "" || len(strings.TrimSpace(res.ReadinessProbeID)) > 256 || strings.ContainsAny(res.ReadinessProbeID, "\r\n\t") {
		return ErrReadyWithoutEvidence
	}
	observedAt := res.ReadinessObservedAt.UTC()
	currentTime := time.Now().UTC()
	if observedAt.IsZero() || observedAt.After(currentTime.Add(5*time.Second)) || currentTime.Sub(observedAt) > 2*time.Minute {
		return ErrReadyWithoutEvidence
	}
	providerURL, providerHost, err := validateProviderTarget(res)
	if err != nil {
		return err
	}
	regionValue := strings.TrimSpace(res.Region)
	if len(regionValue) > 64 || strings.ContainsAny(regionValue, "\r\n\t") {
		return ErrReadyWithoutEvidence
	}
	var region *string
	if regionValue != "" {
		region = &regionValue
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
UPDATE deployments
SET status = 'ready',
    failure_code = '',
    failure_message = '',
    provider_url = $2,
    provider_host = $3,
    provider_region = $4,
    readiness_probe_id = $5,
    readiness_observed_at = $6,
    lease_owner = NULL,
    lease_expires_at = NULL,
    ready_at = $7,
    finished_at = $7,
    updated_at = $7
WHERE id = $1
  AND fencing_token = $8
  AND lease_owner = $9
  AND status = 'provisioning'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at > clock_timestamp()`,
		d.ID, providerURL, providerHost, region, strings.TrimSpace(res.ReadinessProbeID), observedAt, now, d.FencingToken, workerID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	metadata, _ := json.Marshal(map[string]any{
		"status":                   string(StatusReady),
		"configuration_version_id": d.ConfigurationVersionID.String(),
		"readiness_probe_id":       strings.TrimSpace(res.ReadinessProbeID),
		"readiness_observed_at":    observedAt.Format(time.RFC3339Nano),
		"provider_request_id":      d.ProviderRequestID,
	})
	if err := r.insertOutcomeAudit(ctx, tx, d, "deployment.ready", metadata, now); err != nil {
		return err
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

func (r *Repository) insertOutcomeAudit(ctx context.Context, tx pgx.Tx, d Deployment, eventType string, meta []byte, now time.Time) error {
	ct, err := tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
)
SELECT $1, NULL, p.organization_id, $2, 'deployment', $3, NULL, $4, $5
FROM project_environments e
JOIN projects p ON p.id = e.project_id
WHERE e.id = $6`,
		uuid.Must(uuid.NewV7()), eventType, d.ID.String(), meta, now, d.EnvironmentID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

const deploymentColumns = `
id, environment_id, configuration_version_id, target_mode, provider, status,
failure_code, failure_message, attempt_count, max_attempts, idempotency_key, request_fingerprint, provider_request_id,
lease_owner, lease_expires_at, fencing_token, next_attempt_at,
provider_url, provider_host, provider_region, readiness_probe_id, readiness_observed_at,
created_by_identity_user_id, created_at, updated_at, started_at, ready_at, finished_at`

const deploymentSelect = `SELECT ` + deploymentColumns + ` FROM deployments `

func scanDeployment(row interface {
	Scan(dest ...any) error
}) (Deployment, error) {
	var d Deployment
	var mode, provider, status string
	err := row.Scan(
		&d.ID, &d.EnvironmentID, &d.ConfigurationVersionID, &mode, &provider, &status,
		&d.FailureCode, &d.FailureMessage, &d.AttemptCount, &d.MaxAttempts, &d.IdempotencyKey, &d.RequestFingerprint, &d.ProviderRequestID,
		&d.LeaseOwner, &d.LeaseExpiresAt, &d.FencingToken, &d.NextAttemptAt,
		&d.ProviderURL, &d.ProviderHost, &d.ProviderRegion, &d.ReadinessProbeID, &d.ReadinessObservedAt,
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

func safePublicFailure(code string) (string, string) {
	switch code {
	case "provider_unavailable":
		return "provider_unavailable", "deployment provider is unavailable"
	case "provider_ambiguous":
		return "provider_ambiguous", "provider outcome is unknown; reconciliation is required"
	case "provider_retryable":
		return "provider_retryable", "deployment provider reported a retryable failure"
	case "provider_failure":
		return "provider_failure", "deployment provider failed"
	case "invalid_provider_evidence":
		return "invalid_provider_evidence", "deployment provider returned invalid readiness evidence"
	case "provider_unknown":
		return "provider_unknown", "deployment provider returned an unsupported outcome"
	case "max_attempts_exceeded":
		return "max_attempts_exceeded", "deployment exceeded the maximum provider attempts"
	case "legacy_unverified_readiness":
		return "legacy_unverified_readiness", "deployment readiness could not be verified"
	case "provider_error":
		return "provider_error", "deployment provider request failed"
	default:
		return "provider_error", "deployment provider request failed"
	}
}
