package apikey

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

func (r *Repository) Create(ctx context.Context, auth MutationAuth, in CreateInput, requestID string) (IssuedKey, error) {
	scopes, err := ValidateScopes(in.Scopes)
	if err != nil {
		return IssuedKey{}, err
	}
	if in.Name == "" || len(in.Name) > 128 {
		return IssuedKey{}, fmt.Errorf("%w: name", ErrInvalidInput)
	}
	if in.KeyType != TypePublishable && in.KeyType != TypeServerSecret {
		return IssuedKey{}, fmt.Errorf("%w: key type", ErrInvalidInput)
	}
	now := time.Now().UTC()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return IssuedKey{}, fmt.Errorf("%w: expires_at must be future", ErrInvalidInput)
	}

	secret, err := GenerateSecret()
	if err != nil {
		return IssuedKey{}, err
	}
	id := uuid.Must(uuid.NewV7())
	full, prefix, err := FormatFullKey(in.KeyType, id, secret)
	if err != nil {
		return IssuedKey{}, err
	}
	hash := HashSecret(secret)
	rotationGroup := uuid.Must(uuid.NewV7())
	scopesJSON, _ := json.Marshal(scopes)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return IssuedKey{}, err
	}
	defer tx.Rollback(ctx)

	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return IssuedKey{}, err
	}
	if !role.CanManageAPIKeys() {
		return IssuedKey{}, ErrForbidden
	}

	_, err = tx.Exec(ctx, `
INSERT INTO api_keys (
  id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
  created_by_identity_user_id, created_at, expires_at, rotation_group_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, auth.EnvID, string(in.KeyType), in.Name, prefix, hash, scopesJSON,
		auth.ActorID, now, in.ExpiresAt, rotationGroup,
	)
	if err != nil {
		return IssuedKey{}, err
	}

	meta, _ := json.Marshal(map[string]any{
		"key_id":   id.String(),
		"key_type": string(in.KeyType),
		"name":     in.Name,
		"prefix":   prefix,
	})
	if err := r.insertAudit(ctx, tx, auth, "api_key.create", id.String(), requestID, meta, now); err != nil {
		return IssuedKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return IssuedKey{}, err
	}

	rec := KeyRecord{
		ID: id, EnvironmentID: auth.EnvID, KeyType: in.KeyType, Name: in.Name,
		KeyPrefix: prefix, SecretHash: hash, Scopes: scopes,
		CreatedByUserID: auth.ActorID, CreatedAt: now, ExpiresAt: in.ExpiresAt,
		RotationGroupID: rotationGroup,
	}
	return IssuedKey{Record: rec, FullKey: full}, nil
}

func (r *Repository) List(ctx context.Context, auth MutationAuth) ([]KeyRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return nil, err
	}
	if !role.CanManageAPIKeys() {
		return nil, ErrForbidden
	}
	rows, err := tx.Query(ctx, `
SELECT id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
       created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at,
       rotation_group_id, rotated_from_key_id
FROM api_keys
WHERE environment_id = $1
ORDER BY created_at DESC`, auth.EnvID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyRecord
	for rows.Next() {
		rec, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) Get(ctx context.Context, auth MutationAuth, keyID uuid.UUID) (KeyRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return KeyRecord{}, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return KeyRecord{}, err
	}
	if !role.CanManageAPIKeys() {
		return KeyRecord{}, ErrForbidden
	}
	rec, err := scanKey(tx.QueryRow(ctx, `
SELECT id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
       created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at,
       rotation_group_id, rotated_from_key_id
FROM api_keys
WHERE id = $1 AND environment_id = $2`, keyID, auth.EnvID))
	if err != nil {
		return KeyRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return KeyRecord{}, err
	}
	return rec, nil
}

func (r *Repository) Revoke(ctx context.Context, auth MutationAuth, keyID uuid.UUID, requestID string) (KeyRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return KeyRecord{}, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return KeyRecord{}, err
	}
	if !role.CanManageAPIKeys() {
		return KeyRecord{}, ErrForbidden
	}
	rec, err := scanKey(tx.QueryRow(ctx, `
SELECT id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
       created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at,
       rotation_group_id, rotated_from_key_id
FROM api_keys
WHERE id = $1 AND environment_id = $2
FOR UPDATE`, keyID, auth.EnvID))
	if err != nil {
		return KeyRecord{}, err
	}
	now := time.Now().UTC()
	if rec.RevokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return KeyRecord{}, err
		}
		return rec, nil
	}
	_, err = tx.Exec(ctx, `UPDATE api_keys SET revoked_at = $2 WHERE id = $1`, keyID, now)
	if err != nil {
		return KeyRecord{}, err
	}
	rec.RevokedAt = &now
	meta, _ := json.Marshal(map[string]any{"key_id": keyID.String(), "prefix": rec.KeyPrefix})
	if err := r.insertAudit(ctx, tx, auth, "api_key.revoke", keyID.String(), requestID, meta, now); err != nil {
		return KeyRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return KeyRecord{}, err
	}
	return rec, nil
}

func (r *Repository) Rotate(ctx context.Context, auth MutationAuth, keyID uuid.UUID, requestID string) (IssuedKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return IssuedKey{}, err
	}
	defer tx.Rollback(ctx)
	role, err := r.lockAuthChain(ctx, tx, auth)
	if err != nil {
		return IssuedKey{}, err
	}
	if !role.CanManageAPIKeys() {
		return IssuedKey{}, ErrForbidden
	}
	old, err := scanKey(tx.QueryRow(ctx, `
SELECT id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
       created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at,
       rotation_group_id, rotated_from_key_id
FROM api_keys
WHERE id = $1 AND environment_id = $2
FOR UPDATE`, keyID, auth.EnvID))
	if err != nil {
		return IssuedKey{}, err
	}
	now := time.Now().UTC()
	if old.RevokedAt != nil {
		return IssuedKey{}, ErrRevoked
	}
	if old.ExpiresAt != nil && !old.ExpiresAt.After(now) {
		return IssuedKey{}, ErrExpired
	}

	secret, err := GenerateSecret()
	if err != nil {
		return IssuedKey{}, err
	}
	newID := uuid.Must(uuid.NewV7())
	full, prefix, err := FormatFullKey(old.KeyType, newID, secret)
	if err != nil {
		return IssuedKey{}, err
	}
	hash := HashSecret(secret)
	scopesJSON, _ := json.Marshal(old.Scopes)

	_, err = tx.Exec(ctx, `UPDATE api_keys SET revoked_at = $2 WHERE id = $1`, old.ID, now)
	if err != nil {
		return IssuedKey{}, err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO api_keys (
  id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
  created_by_identity_user_id, created_at, expires_at, rotation_group_id, rotated_from_key_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		newID, auth.EnvID, string(old.KeyType), old.Name, prefix, hash, scopesJSON,
		auth.ActorID, now, old.ExpiresAt, old.RotationGroupID, old.ID,
	)
	if err != nil {
		return IssuedKey{}, err
	}
	meta, _ := json.Marshal(map[string]any{
		"old_key_id": old.ID.String(),
		"new_key_id": newID.String(),
		"prefix":     prefix,
	})
	if err := r.insertAudit(ctx, tx, auth, "api_key.rotate", newID.String(), requestID, meta, now); err != nil {
		return IssuedKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return IssuedKey{}, err
	}
	rec := KeyRecord{
		ID: newID, EnvironmentID: auth.EnvID, KeyType: old.KeyType, Name: old.Name,
		KeyPrefix: prefix, SecretHash: hash, Scopes: old.Scopes,
		CreatedByUserID: auth.ActorID, CreatedAt: now, ExpiresAt: old.ExpiresAt,
		RotationGroupID: old.RotationGroupID, RotatedFromKeyID: &old.ID,
	}
	return IssuedKey{Record: rec, FullKey: full}, nil
}

func (r *Repository) LookupByID(ctx context.Context, keyID uuid.UUID) (KeyRecord, error) {
	return scanKey(r.pool.QueryRow(ctx, `
SELECT id, environment_id, key_type, name, key_prefix, secret_hash, scopes,
       created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at,
       rotation_group_id, rotated_from_key_id
FROM api_keys WHERE id = $1`, keyID))
}

func (r *Repository) insertAudit(ctx context.Context, tx pgx.Tx, auth MutationAuth, eventType, targetID, requestID string, meta []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO control_plane_audit_events (
  id, actor_user_id, organization_id, event_type, target_type, target_id, request_id, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		uuid.Must(uuid.NewV7()), auth.ActorID, auth.OrgID, eventType, "api_key", targetID, requestID, meta, now,
	)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanKey(row rowScanner) (KeyRecord, error) {
	var rec KeyRecord
	var keyType string
	var scopesRaw []byte
	var rotatedFrom *uuid.UUID
	err := row.Scan(
		&rec.ID, &rec.EnvironmentID, &keyType, &rec.Name, &rec.KeyPrefix, &rec.SecretHash, &scopesRaw,
		&rec.CreatedByUserID, &rec.CreatedAt, &rec.LastUsedAt, &rec.ExpiresAt, &rec.RevokedAt,
		&rec.RotationGroupID, &rotatedFrom,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return KeyRecord{}, ErrNotFound
	}
	if err != nil {
		return KeyRecord{}, err
	}
	rec.KeyType = KeyType(keyType)
	rec.RotatedFromKeyID = rotatedFrom
	if len(scopesRaw) > 0 {
		_ = json.Unmarshal(scopesRaw, &rec.Scopes)
	}
	if rec.Scopes == nil {
		rec.Scopes = []string{}
	}
	return rec, nil
}
