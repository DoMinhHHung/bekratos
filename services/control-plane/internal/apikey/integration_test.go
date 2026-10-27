package apikey

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupEnv(t *testing.T) (context.Context, *pgxpool.Pool, *Service, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	userID := uuid.Must(uuid.NewV7())
	orgID := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	envID := uuid.Must(uuid.NewV7())
	suffix := uuid.NewString()[:8]

	if _, err := pool.Exec(ctx, `
INSERT INTO organizations (id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1,$2,$2,'active',$3)`, orgID, "key-org-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1,$2,'owner','active')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO projects (id, organization_id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1,$2,$3,$3,'active',$4)`, projectID, orgID, "key-proj-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1,$2,'development','development','active')`, envID, projectID); err != nil {
		t.Fatal(err)
	}

	orgRepo := org.NewRepository(pool)
	svc := NewService(NewRepository(pool), orgRepo)
	return ctx, pool, svc, userID, orgID, projectID, envID
}

func TestCreateStoresHashOnlyAndReturnsFullKeyOnce(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypeServerSecret,
		Name:    "primary",
		Scopes:  []string{ScopeDataRead, ScopeDataWrite},
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.FullKey == "" || issued.Record.ID == uuid.Nil {
		t.Fatal("missing full key or id")
	}
	var hash []byte
	var name string
	if err := pool.QueryRow(ctx, `SELECT secret_hash, name FROM api_keys WHERE id = $1`, issued.Record.ID).Scan(&hash, &name); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 {
		t.Fatalf("hash len=%d", len(hash))
	}
	if string(hash) == issued.FullKey || name != "primary" {
		t.Fatal("plaintext leaked into storage or name mismatch")
	}
	list, err := svc.List(ctx, principal, orgID, projectID, envID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != issued.Record.ID {
		t.Fatal("list mismatch")
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyPrefix == "" || len(got.SecretHash) != 32 {
		t.Fatal("detail missing metadata")
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	ctx, pool, svc, ownerID, orgID, projectID, envID := setupEnv(t)
	roles := []struct {
		role      string
		canManage bool
	}{
		{"admin", true},
		{"developer", false},
		{"viewer", false},
		{"billing", false},
	}
	for _, tc := range roles {
		uid := uuid.Must(uuid.NewV7())
		if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1,$2,$3,'active')`, orgID, uid, tc.role); err != nil {
			t.Fatal(err)
		}
		_, err := svc.Create(ctx, auth.Principal{UserID: uid}, orgID, projectID, envID, CreateInput{
			KeyType: TypePublishable,
			Name:    "r-" + tc.role,
			Scopes:  []string{ScopeDataRead},
		})
		if tc.canManage && err != nil {
			t.Fatalf("role %s should manage: %v", tc.role, err)
		}
		if !tc.canManage && !errors.Is(err, ErrForbidden) {
			t.Fatalf("role %s error=%v; want forbidden", tc.role, err)
		}
	}
	_ = ownerID
}

func TestCrossEnvironmentIsolation(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	otherEnv := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1,$2,'staging','staging','active')`, otherEnv, projectID); err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{UserID: userID}
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypePublishable,
		Name:    "env-a",
		Scopes:  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Get(ctx, principal, orgID, projectID, otherEnv, issued.Record.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-env get error=%v; want not found", err)
	}
	_, err = svc.Revoke(ctx, principal, orgID, projectID, otherEnv, issued.Record.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-env revoke error=%v; want not found", err)
	}
}

func TestVerifyActiveRevokedExpired(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypeServerSecret,
		Name:    "verify",
		Scopes:  []string{ScopeDataRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Verify(ctx, issued.FullKey)
	if err != nil {
		t.Fatal(err)
	}
	if res.EnvironmentID != envID || res.KeyID != issued.Record.ID {
		t.Fatal("verify binding mismatch")
	}

	if _, err := svc.Revoke(ctx, principal, orgID, projectID, envID, issued.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(ctx, issued.FullKey); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked verify error=%v", err)
	}

	expired, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType:   TypePublishable,
		Name:      "will-expire",
		ExpiresAt: ptrTime(time.Now().UTC().Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	pastCreated := time.Now().UTC().Add(-2 * time.Hour)
	pastExpiry := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
UPDATE api_keys
SET created_at = $2, expires_at = $3
WHERE id = $1`, expired.Record.ID, pastCreated, pastExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(ctx, expired.FullKey); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired verify error=%v", err)
	}
}

func TestRotateRevokesOldAndPreservesExpiry(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	exp := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType:   TypeServerSecret,
		Name:      "rot",
		Scopes:    []string{ScopeDataWrite},
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := svc.Rotate(ctx, principal, orgID, projectID, envID, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.FullKey == issued.FullKey || rotated.Record.ID == issued.Record.ID {
		t.Fatal("rotation did not issue distinct key")
	}
	if rotated.Record.RotationGroupID != issued.Record.RotationGroupID {
		t.Fatal("rotation group changed")
	}
	if rotated.Record.ExpiresAt == nil || !rotated.Record.ExpiresAt.Equal(exp) {
		t.Fatalf("expiry not preserved: %v", rotated.Record.ExpiresAt)
	}
	old, err := svc.Get(ctx, principal, orgID, projectID, envID, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.RevokedAt == nil {
		t.Fatal("old key not revoked")
	}
	if _, err := svc.Verify(ctx, issued.FullKey); !errors.Is(err, ErrRevoked) {
		t.Fatal("old secret still verifies")
	}
	if _, err := svc.Verify(ctx, rotated.FullKey); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRotateAndRevoke(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypePublishable,
		Name:    "race",
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Rotate(ctx, principal, orgID, projectID, envID, issued.Record.ID)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.Revoke(ctx, principal, orgID, projectID, envID, issued.Record.ID)
		errs <- err
	}()
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success < 1 {
		t.Fatal("expected at least one successful mutation")
	}
	old, err := svc.Get(ctx, principal, orgID, projectID, envID, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.RevokedAt == nil {
		t.Fatal("source key must end revoked")
	}
}

func TestRejectUnknownScopesAndPastExpiry(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	_, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypePublishable,
		Name:    "bad-scope",
		Scopes:  []string{"admin"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown scope error=%v", err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	_, err = svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType:   TypePublishable,
		Name:      "past",
		ExpiresAt: &past,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("past expiry error=%v", err)
	}
}

func TestAuditContainsNoSecret(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	issued, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		KeyType: TypeServerSecret,
		Name:    "audited",
	})
	if err != nil {
		t.Fatal(err)
	}
	var meta []byte
	if err := pool.QueryRow(ctx, `
SELECT metadata FROM control_plane_audit_events
WHERE target_id = $1 AND event_type = 'api_key.create'
ORDER BY created_at DESC LIMIT 1`, issued.Record.ID.String()).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if string(meta) == "" {
		t.Fatal("empty audit metadata")
	}
	if containsSecretLeak(string(meta), issued.FullKey) {
		t.Fatal("audit metadata contains secret material")
	}
}

func containsSecretLeak(meta, fullKey string) bool {
	if fullKey != "" && (len(fullKey) > 20 && contains(meta, fullKey)) {
		return true
	}
	return contains(meta, "secret_hash") || contains(meta, "full_key")
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

func ptrTime(t time.Time) *time.Time { return &t }
