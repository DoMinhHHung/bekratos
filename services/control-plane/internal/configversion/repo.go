package configversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
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

type MutationAuth struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	ActorID   uuid.UUID
}

type CreateResult struct {
	Version Version
	Job     *ApplyJob
	Reused  bool
}

func (r *Repository) GetVersion(ctx context.Context, envID, versionID uuid.UUID) (Version, error) {
	const q = `
SELECT id, environment_id, version_number, parent_version_id, expected_active_version_id,
       schema_revision, pin_revision, manifest_format_version, manifest_canonical, manifest_sha256,
       compatibility, state, COALESCE(failure_code,''), COALESCE(failure_message,''),
       confirmation_required, confirmed_at, confirmed_by_user_id, COALESCE(confirmation_hash,''),
       activated_at, created_by_user_id, created_at, updated_at, COALESCE(idempotency_key,'')
FROM environment_configuration_versions
WHERE id = $1 AND environment_id = $2`
	return scanVersion(r.pool.QueryRow(ctx, q, versionID, envID))
}

func (r *Repository) ListVersions(ctx context.Context, envID uuid.UUID, limit int) ([]Version, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, environment_id, version_number, parent_version_id, expected_active_version_id,
       schema_revision, pin_revision, manifest_format_version, manifest_canonical, manifest_sha256,
       compatibility, state, COALESCE(failure_code,''), COALESCE(failure_message,''),
       confirmation_required, confirmed_at, confirmed_by_user_id, COALESCE(confirmation_hash,''),
       activated_at, created_by_user_id, created_at, updated_at, COALESCE(idempotency_key,'')
FROM environment_configuration_versions
WHERE environment_id = $1
ORDER BY version_number DESC
LIMIT $2`, envID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) GetJobByVersion(ctx context.Context, envID, versionID uuid.UUID) (ApplyJob, error) {
	const q = `
SELECT id, environment_id, configuration_version_id, state, attempt_count, max_attempts,
       lease_owner, lease_expires_at, fencing_token, next_attempt_at,
       COALESCE(last_error_code,''), COALESCE(last_error_message,''), apply_request_id,
       created_at, updated_at
FROM environment_configuration_apply_jobs
WHERE configuration_version_id = $1 AND environment_id = $2`
	return scanJob(r.pool.QueryRow(ctx, q, versionID, envID))
}

func (r *Repository) CreateCandidate(
	ctx context.Context,
	auth MutationAuth,
	in CreateCandidateInput,
	requestID string,
) (CreateResult, error) {
	if in.ExpectedSchemaRevision < 0 || in.ExpectedPinRevision < 0 {
		return CreateResult{}, ErrInvalidInput
	}
	if in.IdempotencyKey == "" {
		return CreateResult{}, fmt.Errorf("%w: idempotency_key required", ErrInvalidInput)
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
	if !role.CanMutateSchema() {
		return CreateResult{}, ErrForbidden
	}

	if existing, ok, err := r.findIdempotent(ctx, tx, auth.EnvID, in.IdempotencyKey); err != nil {
		return CreateResult{}, err
	} else if ok {
		if !idempotentMatches(existing, in) {
			return CreateResult{}, ErrConflictIdempotency
		}
		job, jobErr := r.getJobTx(ctx, tx, auth.EnvID, existing.ID)
		if jobErr != nil && !errors.Is(jobErr, ErrNotFound) {
			return CreateResult{}, jobErr
		}
		var jp *ApplyJob
		if jobErr == nil {
			jp = &job
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateResult{}, err
		}
		return CreateResult{Version: existing, Job: jp, Reused: true}, nil
	}

	snap, err := r.loadSnapshotLocked(ctx, tx, auth.EnvID)
	if err != nil {
		return CreateResult{}, err
	}
	if snap.SchemaRevision != in.ExpectedSchemaRevision || snap.PinRevision != in.ExpectedPinRevision {
		return CreateResult{}, ErrConflictRevision
	}
	if !uuidPtrEqual(snap.ActiveVersionID, in.ExpectedActiveVersionID) {
		return CreateResult{}, ErrConflictRevision
	}

	var activeManifest *Manifest
	if snap.ActiveVersionID != nil {
		av, err := r.getVersionTx(ctx, tx, auth.EnvID, *snap.ActiveVersionID)
		if err != nil {
			return CreateResult{}, err
		}
		m, err := DecodeManifest(av.ManifestCanonical)
		if err != nil {
			return CreateResult{}, err
		}
		activeManifest = &m
	}

	manifest := Manifest{
		ManifestFormatVersion: ManifestFormatVersion,
		EnvironmentID:         auth.EnvID,
		SchemaRevision:        snap.SchemaRevision,
		PinRevision:           snap.PinRevision,
		Schema:                snap.Schema,
		ModulePins:            snap.Pins,
	}
	canonical, hash, err := CanonicalizeAndHash(manifest)
	if err != nil {
		return CreateResult{}, err
	}
	compat := Classify(activeManifest, &manifest)

	var nextNumber int64 = 1
	err = tx.QueryRow(ctx, `
SELECT COALESCE(MAX(version_number), 0) + 1
FROM environment_configuration_versions WHERE environment_id = $1`, auth.EnvID).Scan(&nextNumber)
	if err != nil {
		return CreateResult{}, err
	}

	now := time.Now().UTC()
	versionID := uuid.Must(uuid.NewV7())
	state := StateQueued
	confirmRequired := compat == CompatBreakingOrDestructive || compat == CompatMigrationRequired
	if confirmRequired {
		state = StateAwaitingConfirmation
	}

	_, err = tx.Exec(ctx, `
INSERT INTO environment_configuration_versions (
  id, environment_id, version_number, parent_version_id, expected_active_version_id,
  schema_revision, pin_revision, manifest_format_version, manifest_canonical, manifest_sha256,
  compatibility, state, confirmation_required, created_by_user_id, created_at, updated_at, idempotency_key
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15,$16
)`,
		versionID, auth.EnvID, nextNumber, snap.ActiveVersionID, in.ExpectedActiveVersionID,
		snap.SchemaRevision, snap.PinRevision, ManifestFormatVersion, canonical, hash,
		string(compat), string(state), confirmRequired, auth.ActorID, now, in.IdempotencyKey,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return CreateResult{}, ErrConflict
		}
		return CreateResult{}, err
	}

	var job *ApplyJob
	if state == StateQueued {
		j, err := r.insertJobTx(ctx, tx, auth.EnvID, versionID, now)
		if err != nil {
			return CreateResult{}, err
		}
		job = &j
	}

	meta, _ := json.Marshal(map[string]any{
		"version_id":      versionID.String(),
		"version_number":  nextNumber,
		"compatibility":   string(compat),
		"manifest_sha256": hash,
		"state":           string(state),
	})
	if err := r.insertAudit(ctx, tx, auth, "config.version.create", versionID.String(), requestID, meta, now); err != nil {
		return CreateResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}

	v := Version{
		ID: versionID, EnvironmentID: auth.EnvID, VersionNumber: nextNumber,
		ParentVersionID: snap.ActiveVersionID, ExpectedActiveVersionID: in.ExpectedActiveVersionID,
		SchemaRevision: snap.SchemaRevision, PinRevision: snap.PinRevision,
		ManifestFormatVersion: ManifestFormatVersion, ManifestCanonical: canonical, ManifestSHA256: hash,
		Compatibility: compat, State: state, ConfirmationRequired: confirmRequired,
		CreatedByUserID: auth.ActorID, CreatedAt: now, UpdatedAt: now, IdempotencyKey: in.IdempotencyKey,
	}
	return CreateResult{Version: v, Job: job}, nil
}

func (r *Repository) ConfirmCandidate(ctx context.Context, auth MutationAuth, in ConfirmInput, requestID string) (Version, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)

	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return Version{}, err
	}
	if !role.CanMutateSchema() {
		return Version{}, ErrForbidden
	}

	v, err := r.getVersionTx(ctx, tx, auth.EnvID, in.VersionID)
	if err != nil {
		return Version{}, err
	}
	if v.State != StateAwaitingConfirmation {
		return Version{}, ErrInvalidTransition
	}
	if v.ManifestSHA256 != in.ExpectedManifestSHA256 {
		return Version{}, ErrConfirmationMismatch
	}
	if !uuidPtrEqual(v.ExpectedActiveVersionID, in.ExpectedActiveVersionID) {
		return Version{}, ErrConfirmationMismatch
	}

	var activeID *uuid.UUID
	err = tx.QueryRow(ctx, `
SELECT active_configuration_version_id FROM project_environments WHERE id = $1 FOR UPDATE`,
		auth.EnvID).Scan(&activeID)
	if err != nil {
		return Version{}, err
	}
	if !uuidPtrEqual(activeID, v.ExpectedActiveVersionID) {
		return Version{}, ErrConflictRevision
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, confirmed_at = $3, confirmed_by_user_id = $4, confirmation_hash = $5, updated_at = $3
WHERE id = $1 AND environment_id = $6 AND state = $7`,
		v.ID, string(StateQueued), now, auth.ActorID, in.ExpectedManifestSHA256, auth.EnvID, string(StateAwaitingConfirmation),
	)
	if err != nil {
		return Version{}, err
	}

	job, err := r.insertJobTx(ctx, tx, auth.EnvID, v.ID, now)
	if err != nil {
		return Version{}, err
	}
	_ = job

	meta, _ := json.Marshal(map[string]any{
		"version_id":      v.ID.String(),
		"manifest_sha256": v.ManifestSHA256,
	})
	if err := r.insertAudit(ctx, tx, auth, "config.version.confirm", v.ID.String(), requestID, meta, now); err != nil {
		return Version{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Version{}, err
	}
	v.State = StateQueued
	v.ConfirmedAt = &now
	v.ConfirmedByUserID = &auth.ActorID
	v.ConfirmationHash = in.ExpectedManifestSHA256
	v.UpdatedAt = now
	return v, nil
}

func (r *Repository) ClaimJobs(ctx context.Context, owner string, lease time.Duration, limit int) ([]ApplyJob, error) {
	if limit <= 0 {
		limit = 1
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	rows, err := tx.Query(ctx, `
SELECT environment_id
FROM (
    SELECT DISTINCT ON (j.environment_id)
           j.environment_id,
           j.next_attempt_at,
           j.created_at,
           j.id
    FROM environment_configuration_apply_jobs j
    WHERE (
            (j.state = 'queued' AND j.next_attempt_at <= $1)
         OR (j.state = 'leased' AND j.lease_expires_at <= $1)
          )
      AND NOT EXISTS (
            SELECT 1
            FROM environment_configuration_apply_jobs active
            WHERE active.environment_id = j.environment_id
              AND active.state = 'leased'
              AND active.lease_expires_at > $1
          )
    ORDER BY j.environment_id, j.next_attempt_at, j.created_at, j.id
) candidates
ORDER BY next_attempt_at, created_at, id
LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	var envIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		envIDs = append(envIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	leaseExp := now.Add(lease)
	var claimed []ApplyJob
	for _, envID := range envIDs {
		var lockedEnvID uuid.UUID
		err := tx.QueryRow(ctx, `
SELECT id FROM project_environments WHERE id = $1 FOR UPDATE`, envID).Scan(&lockedEnvID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}

		var activeLease bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM environment_configuration_apply_jobs
    WHERE environment_id = $1
      AND state = 'leased'
      AND lease_expires_at > $2
)`, envID, now).Scan(&activeLease); err != nil {
			return nil, err
		}
		if activeLease {
			continue
		}

		var jobID uuid.UUID
		var attempts, maxAttempts int
		err = tx.QueryRow(ctx, `
SELECT id, attempt_count, max_attempts
FROM environment_configuration_apply_jobs
WHERE environment_id = $1
  AND ((state = 'queued' AND next_attempt_at <= $2)
    OR (state = 'leased' AND lease_expires_at <= $2))
ORDER BY next_attempt_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`, envID, now).Scan(&jobID, &attempts, &maxAttempts)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if attempts >= maxAttempts {
			ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'failed',
    last_error_code = 'max_attempts',
    last_error_message = 'maximum apply attempts exceeded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    updated_at = $2
WHERE id = $1`, jobID, now)
			if err != nil {
				return nil, err
			}
			if ct.RowsAffected() != 1 {
				return nil, ErrStaleWorker
			}
			ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions v
SET state = $2,
    failure_code = 'max_attempts',
    failure_message = 'maximum apply attempts exceeded',
    updated_at = $3
FROM environment_configuration_apply_jobs j
WHERE j.id = $1
  AND v.id = j.configuration_version_id
  AND v.state IN ('queued', 'applying')`, jobID, string(StateFailed), now)
			if err != nil {
				return nil, err
			}
			if ct.RowsAffected() != 1 {
				return nil, ErrInvalidTransition
			}
			continue
		}

		ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'leased',
    lease_owner = $2,
    lease_expires_at = $3,
    fencing_token = fencing_token + 1,
    attempt_count = attempt_count + 1,
    updated_at = $4
WHERE id = $1`, jobID, owner, leaseExp, now)
		if err != nil {
			return nil, err
		}
		if ct.RowsAffected() != 1 {
			return nil, ErrStaleWorker
		}
		j, err := scanJob(tx.QueryRow(ctx, `
SELECT id, environment_id, configuration_version_id, state, attempt_count, max_attempts,
       lease_owner, lease_expires_at, fencing_token, next_attempt_at,
       COALESCE(last_error_code,''), COALESCE(last_error_message,''), apply_request_id,
       created_at, updated_at
FROM environment_configuration_apply_jobs WHERE id = $1`, jobID))
		if err != nil {
			return nil, err
		}
		ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, updated_at = $3
WHERE id = $1 AND state IN ('queued', 'applying')`,
			j.ConfigurationVersionID, string(StateApplying), now)
		if err != nil {
			return nil, err
		}
		if ct.RowsAffected() != 1 {
			return nil, ErrInvalidTransition
		}
		claimed = append(claimed, j)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *Repository) ScheduleJobRetry(ctx context.Context, job ApplyJob, fencing int64, code, message string, lease time.Duration) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	if job.AttemptCount >= job.MaxAttempts {
		return r.releaseJobTerminalTx(ctx, tx, job, fencing, JobFailed, "max_attempts", "maximum apply attempts exceeded", now)
	}

	shift := job.AttemptCount
	if shift < 0 {
		shift = 0
	}
	if shift > 5 {
		shift = 5
	}
	backoff := time.Duration(1<<shift) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	next := now.Add(backoff)
	ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'queued',
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = $3,
    last_error_code = $4,
    last_error_message = $5,
    updated_at = $6
WHERE id = $1 AND fencing_token = $2 AND state = 'leased'`,
		job.ID, fencing, next, code, message, now)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, updated_at = $3
WHERE id = $1 AND state = $4`,
		job.ConfigurationVersionID, string(StateQueued), now, string(StateApplying))
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrInvalidTransition
	}
	return tx.Commit(ctx)
}

func (r *Repository) ReleaseJobTerminal(ctx context.Context, job ApplyJob, fencing int64, state JobState, code, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return r.releaseJobTerminalTx(ctx, tx, job, fencing, state, code, message, time.Now().UTC())
}

func (r *Repository) releaseJobTerminalTx(ctx context.Context, tx pgx.Tx, job ApplyJob, fencing int64, state JobState, code, message string, now time.Time) error {
	versionState := StateFailed
	switch state {
	case JobFailed:
		versionState = StateFailed
	case JobBlocked:
		versionState = StateBlocked
	default:
		return ErrInvalidTransition
	}
	ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = $3,
    last_error_code = $4,
    last_error_message = $5,
    lease_owner = NULL,
    lease_expires_at = NULL,
    updated_at = $6
WHERE id = $1 AND fencing_token = $2 AND state = 'leased'`,
		job.ID, fencing, string(state), code, message, now)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, failure_code = $3, failure_message = $4, updated_at = $5
WHERE id = $1 AND state IN ($6, $7)`,
		job.ConfigurationVersionID, string(versionState), code, message, now,
		string(StateApplying), string(StateQueued))
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrInvalidTransition
	}
	return tx.Commit(ctx)
}

func (r *Repository) MarkJobNeedsReconciliation(ctx context.Context, job ApplyJob, fencing int64, code, message string) error {
	if code == "" {
		code = "reconciliation_required"
	}
	if message == "" {
		message = "runtime apply requires reconciliation; active pointer not advanced"
	}
	return r.MarkJobBlocked(ctx, job, fencing, code, message)
}

func (r *Repository) MarkJobBlocked(ctx context.Context, job ApplyJob, fencing int64, code, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'blocked', last_error_code = $3, last_error_message = $4,
    lease_owner = NULL, lease_expires_at = NULL, updated_at = $5
WHERE id = $1 AND fencing_token = $2 AND state = 'leased'`,
		job.ID, fencing, code, message, now)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}
	ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, failure_code = $3, failure_message = $4, updated_at = $5
WHERE id = $1 AND state = $6`,
		job.ConfigurationVersionID, string(StateBlocked), code, message, now, string(StateApplying))
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrInvalidTransition
	}
	return tx.Commit(ctx)
}

func (r *Repository) ActivateAfterApply(
	ctx context.Context,
	job ApplyJob,
	fencing int64,
	expectedSHA256 string,
	requestID string,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	var activeID *uuid.UUID
	err = tx.QueryRow(ctx, `
SELECT active_configuration_version_id
FROM project_environments
WHERE id = $1
FOR UPDATE`, job.EnvironmentID).Scan(&activeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	var jState string
	var jFencing int64
	var envID, versionID uuid.UUID
	var applyRequestID string
	var leaseActive bool
	err = tx.QueryRow(ctx, `
SELECT state, fencing_token, environment_id, configuration_version_id,
       apply_request_id, lease_expires_at > clock_timestamp()
FROM environment_configuration_apply_jobs WHERE id = $1 FOR UPDATE`, job.ID).
		Scan(&jState, &jFencing, &envID, &versionID, &applyRequestID, &leaseActive)
	if err != nil {
		return err
	}
	if jState != string(JobLeased) || jFencing != fencing ||
		envID != job.EnvironmentID || versionID != job.ConfigurationVersionID ||
		applyRequestID != job.ApplyRequestID || !leaseActive {
		return ErrStaleWorker
	}

	var v Version
	v, err = r.getVersionTx(ctx, tx, envID, versionID)
	if err != nil {
		return err
	}
	if v.State != StateApplying && v.State != StateQueued {
		return ErrInvalidTransition
	}
	if v.ManifestSHA256 != expectedSHA256 {
		return ErrValidation
	}
	if !uuidPtrEqual(activeID, v.ExpectedActiveVersionID) {
		return ErrConflictRevision
	}

	if activeID != nil {
		_, err = tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, updated_at = $3
WHERE id = $1 AND environment_id = $4 AND state = $5`,
			*activeID, string(StateSuperseded), now, envID, string(StateActive))
		if err != nil {
			return err
		}
	}

	ct, err := tx.Exec(ctx, `
UPDATE environment_configuration_versions
SET state = $2, activated_at = $3, updated_at = $3
WHERE id = $1 AND environment_id = $4 AND state IN ($5, $6) AND manifest_sha256 = $7`,
		versionID, string(StateActive), now, envID, string(StateApplying), string(StateQueued), expectedSHA256)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrInvalidTransition
	}

	ct, err = tx.Exec(ctx, `
UPDATE project_environments
SET active_configuration_version_id = $2, updated_at = $3
WHERE id = $1 AND active_configuration_version_id IS NOT DISTINCT FROM $4`,
		envID, versionID, now, v.ExpectedActiveVersionID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrConflictRevision
	}

	ct, err = tx.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'succeeded', lease_owner = NULL, lease_expires_at = NULL, updated_at = $2
WHERE id = $1 AND fencing_token = $3 AND state = 'leased' AND lease_expires_at > clock_timestamp()`,
		job.ID, now, fencing)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrStaleWorker
	}

	meta, _ := json.Marshal(map[string]any{
		"version_id":      versionID.String(),
		"manifest_sha256": expectedSHA256,
	})
	ct, err = tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
)
SELECT $1, NULL, p.organization_id, $2, 'configuration_version', $3, $4, $5, $6
FROM project_environments e
JOIN projects p ON p.id = e.project_id
WHERE e.id = $7`,
		uuid.Must(uuid.NewV7()), "config.version.activate", versionID.String(), requestID, meta, now, envID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
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

func (r *Repository) loadSnapshotLocked(ctx context.Context, tx pgx.Tx, envID uuid.UUID) (Snapshot, error) {
	var activeID *uuid.UUID
	err := tx.QueryRow(ctx, `
SELECT active_configuration_version_id FROM project_environments WHERE id = $1`, envID).Scan(&activeID)
	if err != nil {
		return Snapshot{}, err
	}

	var pinRev int64
	err = tx.QueryRow(ctx, `
SELECT revision FROM environment_module_pin_state WHERE environment_id = $1 FOR UPDATE`, envID).Scan(&pinRev)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
INSERT INTO environment_module_pin_state (environment_id, revision, updated_at)
VALUES ($1, 0, $2)
ON CONFLICT (environment_id) DO NOTHING`, envID, time.Now().UTC())
		if err != nil {
			return Snapshot{}, err
		}
		err = tx.QueryRow(ctx, `
SELECT revision FROM environment_module_pin_state WHERE environment_id = $1 FOR UPDATE`, envID).Scan(&pinRev)
	}
	if err != nil {
		return Snapshot{}, err
	}

	rows, err := tx.Query(ctx, `
SELECT p.module_slug, p.release_id, r.version, r.api_contract_version
FROM environment_module_pins p
JOIN module_releases r ON r.id = p.release_id
WHERE p.environment_id = $1
ORDER BY p.module_slug
FOR UPDATE OF p`, envID)
	if err != nil {
		return Snapshot{}, err
	}
	var pins []ModulePinSnapshot
	for rows.Next() {
		var p ModulePinSnapshot
		if err := rows.Scan(&p.ModuleSlug, &p.ReleaseID, &p.Version, &p.APIContractVersion); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		pins = append(pins, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}
	if pins == nil {
		pins = []ModulePinSnapshot{}
	}

	var schemaRev int64
	var raw []byte
	err = tx.QueryRow(ctx, `
SELECT revision, schema_json FROM environment_schema_drafts WHERE environment_id = $1 FOR UPDATE`,
		envID).Scan(&schemaRev, &raw)
	doc := schema.EmptyDocument()
	if errors.Is(err, pgx.ErrNoRows) {
		schemaRev = 0
	} else if err != nil {
		return Snapshot{}, err
	} else if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return Snapshot{}, err
		}
		if doc.Entities == nil {
			doc.Entities = map[string]schema.EntityDefinition{}
		}
	}

	return Snapshot{
		SchemaRevision:  schemaRev,
		PinRevision:     pinRev,
		Schema:          doc,
		Pins:            pins,
		ActiveVersionID: activeID,
	}, nil
}

func (r *Repository) insertJobTx(ctx context.Context, tx pgx.Tx, envID, versionID uuid.UUID, now time.Time) (ApplyJob, error) {
	jobID := uuid.Must(uuid.NewV7())
	applyReq := uuid.Must(uuid.NewV7()).String()
	_, err := tx.Exec(ctx, `
INSERT INTO environment_configuration_apply_jobs (
  id, environment_id, configuration_version_id, state, attempt_count, max_attempts,
  fencing_token, next_attempt_at, apply_request_id, created_at, updated_at
) VALUES ($1,$2,$3,'queued',0,5,0,$4,$5,$4,$4)`,
		jobID, envID, versionID, now, applyReq)
	if err != nil {
		return ApplyJob{}, err
	}
	return ApplyJob{
		ID: jobID, EnvironmentID: envID, ConfigurationVersionID: versionID,
		State: JobQueued, MaxAttempts: 5, NextAttemptAt: now, ApplyRequestID: applyReq,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (r *Repository) insertAudit(ctx context.Context, tx pgx.Tx, auth MutationAuth, eventType, targetID, requestID string, meta []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		uuid.Must(uuid.NewV7()), auth.ActorID, auth.OrgID, eventType, "configuration_version", targetID, requestID, meta, now,
	)
	return err
}

func (r *Repository) findIdempotent(ctx context.Context, tx pgx.Tx, envID uuid.UUID, key string) (Version, bool, error) {
	v, err := scanVersion(tx.QueryRow(ctx, `
SELECT id, environment_id, version_number, parent_version_id, expected_active_version_id,
       schema_revision, pin_revision, manifest_format_version, manifest_canonical, manifest_sha256,
       compatibility, state, COALESCE(failure_code,''), COALESCE(failure_message,''),
       confirmation_required, confirmed_at, confirmed_by_user_id, COALESCE(confirmation_hash,''),
       activated_at, created_by_user_id, created_at, updated_at, COALESCE(idempotency_key,'')
FROM environment_configuration_versions
WHERE environment_id = $1 AND idempotency_key = $2`, envID, key))
	if errors.Is(err, ErrNotFound) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

func idempotentMatches(v Version, in CreateCandidateInput) bool {
	if v.SchemaRevision != in.ExpectedSchemaRevision || v.PinRevision != in.ExpectedPinRevision {
		return false
	}
	return uuidPtrEqual(v.ExpectedActiveVersionID, in.ExpectedActiveVersionID)
}

func (r *Repository) getVersionTx(ctx context.Context, tx pgx.Tx, envID, versionID uuid.UUID) (Version, error) {
	return scanVersion(tx.QueryRow(ctx, `
SELECT id, environment_id, version_number, parent_version_id, expected_active_version_id,
       schema_revision, pin_revision, manifest_format_version, manifest_canonical, manifest_sha256,
       compatibility, state, COALESCE(failure_code,''), COALESCE(failure_message,''),
       confirmation_required, confirmed_at, confirmed_by_user_id, COALESCE(confirmation_hash,''),
       activated_at, created_by_user_id, created_at, updated_at, COALESCE(idempotency_key,'')
FROM environment_configuration_versions
WHERE id = $1 AND environment_id = $2
FOR UPDATE`, versionID, envID))
}

func (r *Repository) getJobTx(ctx context.Context, tx pgx.Tx, envID, versionID uuid.UUID) (ApplyJob, error) {
	return scanJob(tx.QueryRow(ctx, `
SELECT id, environment_id, configuration_version_id, state, attempt_count, max_attempts,
       lease_owner, lease_expires_at, fencing_token, next_attempt_at,
       COALESCE(last_error_code,''), COALESCE(last_error_message,''), apply_request_id,
       created_at, updated_at
FROM environment_configuration_apply_jobs
WHERE configuration_version_id = $1 AND environment_id = $2`, versionID, envID))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(row rowScanner) (Version, error) {
	var v Version
	var parent, expected, confirmedBy *uuid.UUID
	var confirmedAt, activatedAt *time.Time
	var compat, state string
	err := row.Scan(
		&v.ID, &v.EnvironmentID, &v.VersionNumber, &parent, &expected,
		&v.SchemaRevision, &v.PinRevision, &v.ManifestFormatVersion, &v.ManifestCanonical, &v.ManifestSHA256,
		&compat, &state, &v.FailureCode, &v.FailureMessage,
		&v.ConfirmationRequired, &confirmedAt, &confirmedBy, &v.ConfirmationHash,
		&activatedAt, &v.CreatedByUserID, &v.CreatedAt, &v.UpdatedAt, &v.IdempotencyKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	v.ParentVersionID = parent
	v.ExpectedActiveVersionID = expected
	v.Compatibility = CompatibilityClass(compat)
	v.State = VersionState(state)
	v.ConfirmedAt = confirmedAt
	v.ConfirmedByUserID = confirmedBy
	v.ActivatedAt = activatedAt
	return v, nil
}

func scanJob(row rowScanner) (ApplyJob, error) {
	var j ApplyJob
	var leaseOwner *string
	var leaseExp *time.Time
	var state string
	err := row.Scan(
		&j.ID, &j.EnvironmentID, &j.ConfigurationVersionID, &state, &j.AttemptCount, &j.MaxAttempts,
		&leaseOwner, &leaseExp, &j.FencingToken, &j.NextAttemptAt,
		&j.LastErrorCode, &j.LastErrorMessage, &j.ApplyRequestID,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplyJob{}, ErrNotFound
	}
	if err != nil {
		return ApplyJob{}, err
	}
	j.State = JobState(state)
	if leaseOwner != nil {
		j.LeaseOwner = *leaseOwner
	}
	j.LeaseExpiresAt = leaseExp
	return j, nil
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (r *Repository) GetEnvironmentStatuses(ctx context.Context, orgID, projectID, envID uuid.UUID) (orgStatus, projectStatus, envStatus string, err error) {
	err = r.pool.QueryRow(ctx, `
SELECT o.status, p.status, e.status
FROM organizations o
JOIN projects p ON p.organization_id = o.id
JOIN project_environments e ON e.project_id = p.id
WHERE o.id = $1 AND p.id = $2 AND e.id = $3`,
		orgID, projectID, envID,
	).Scan(&orgStatus, &projectStatus, &envStatus)
	if err != nil {
		return "", "", "", ErrNotFound
	}
	if orgStatus != "active" || projectStatus != "active" || envStatus != "active" {
		return orgStatus, projectStatus, envStatus, ErrInactive
	}
	return orgStatus, projectStatus, envStatus, nil
}
