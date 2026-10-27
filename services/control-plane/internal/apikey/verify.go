package apikey

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) Verify(ctx context.Context, fullKey string) (ResolveResult, error) {
	keyType, id, secret, err := ParseFullKey(fullKey)
	if err != nil {
		return ResolveResult{}, err
	}
	rec, err := r.LookupByID(ctx, id)
	if err != nil {
		return ResolveResult{}, ErrInvalidSecret
	}
	if rec.KeyType != keyType {
		return ResolveResult{}, ErrInvalidSecret
	}
	if !ConstantTimeEqualHash(rec.SecretHash, HashSecret(secret)) {
		return ResolveResult{}, ErrInvalidSecret
	}
	now := time.Now().UTC()
	status := rec.Status(now)
	if status == StatusRevoked {
		return ResolveResult{}, ErrRevoked
	}
	if status == StatusExpired {
		return ResolveResult{}, ErrExpired
	}

	var orgStatus, projectStatus, envStatus string
	err = r.pool.QueryRow(ctx, `
SELECT o.status, p.status, e.status
FROM project_environments e
JOIN projects p ON p.id = e.project_id
JOIN organizations o ON o.id = p.organization_id
WHERE e.id = $1`, rec.EnvironmentID).Scan(&orgStatus, &projectStatus, &envStatus)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ResolveResult{}, ErrNotFound
		}
		return ResolveResult{}, err
	}
	if orgStatus != "active" || projectStatus != "active" || envStatus != "active" {
		return ResolveResult{}, ErrInactive
	}

	return ResolveResult{
		KeyID:         rec.ID,
		EnvironmentID: rec.EnvironmentID,
		KeyType:       rec.KeyType,
		Scopes:        append([]string(nil), rec.Scopes...),
		Status:        StatusActive,
	}, nil
}
