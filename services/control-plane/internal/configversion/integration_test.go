package configversion

import (
	"context"
	"errors"
	"os"
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
VALUES ($1,$2,$2,'active',$3)`, orgID, "cfg-org-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1,$2,'owner','active')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO projects (id, organization_id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1,$2,$3,$3,'active',$4)`, projectID, orgID, "cfg-proj-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1,$2,'development','development','active')`, envID, projectID); err != nil {
		t.Fatal(err)
	}

	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	svc := NewService(repo, orgRepo, NewUnavailableApplyAdapter())
	return ctx, pool, svc, userID, orgID, projectID, envID
}

func TestConfigVersionUnavailableRuntimeLeavesActiveUnchanged(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}

	res, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedActiveVersionID: nil,
		ExpectedSchemaRevision:  0,
		ExpectedPinRevision:     0,
		IdempotencyKey:          "idem-1-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Version.State != StateQueued && res.Version.State != StateAwaitingConfirmation {
		t.Fatalf("state=%s", res.Version.State)
	}

	if res.Version.State == StateAwaitingConfirmation {
		res.Version, err = svc.Confirm(ctx, principal, orgID, projectID, envID, ConfirmInput{
			VersionID:               res.Version.ID,
			ExpectedManifestSHA256:  res.Version.ManifestSHA256,
			ExpectedActiveVersionID: nil,
		})
		if err != nil {
			t.Fatalf("confirm: %v", err)
		}
	}

	n, err := svc.ProcessOnce(ctx, "test-worker", 10*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if n < 0 {
		t.Fatal("negative processed")
	}

	v, err := svc.GetVersion(ctx, principal, orgID, projectID, envID, res.Version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State == StateActive {
		t.Fatal("must not activate without runtime acknowledgement")
	}
	if v.State != StateBlocked && v.State != StateFailed && v.State != StateApplying && v.State != StateQueued {
		t.Logf("state after process=%s (blocked expected when job claimed)", v.State)
	}

	var active *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT active_configuration_version_id FROM project_environments WHERE id = $1`, envID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatalf("active pointer must stay null, got %v", *active)
	}
}

func TestConfigVersionStaleBaseRejected(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}

	_, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedSchemaRevision: 99,
		ExpectedPinRevision:    0,
		IdempotencyKey:         "stale-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrConflictRevision) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

func TestConfigVersionIdempotentReuse(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	key := "idem-reuse-" + uuid.NewString()
	in := CreateCandidateInput{
		ExpectedSchemaRevision: 0,
		ExpectedPinRevision:    0,
		IdempotencyKey:         key,
	}
	a, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Reused || a.Version.ID != b.Version.ID {
		t.Fatalf("expected idempotent reuse, reused=%v a=%s b=%s", b.Reused, a.Version.ID, b.Version.ID)
	}
}

func TestConfigVersionActivationWithRecordingAdapter(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)

	adapter := &RecordingApplyAdapter{}
	svc := NewService(repo, orgRepo, adapter)

	res, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedSchemaRevision: 0,
		ExpectedPinRevision:    0,
		IdempotencyKey:         "act-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	v := res.Version
	if v.State == StateAwaitingConfirmation {
		v, err = svc.Confirm(ctx, principal, orgID, projectID, envID, ConfirmInput{
			VersionID:              v.ID,
			ExpectedManifestSHA256: v.ManifestSHA256,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	adapter.Result = ApplyResult{
		Accepted:       true,
		AppliedVersion: v.ID,
		AppliedSHA256:  v.ManifestSHA256,
	}

	if _, err := svc.ProcessOnce(ctx, "activate-worker", 15*time.Second, 5); err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetVersion(ctx, principal, orgID, projectID, envID, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateActive {
		t.Fatalf("expected active, got %s", got.State)
	}
	var active uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT active_configuration_version_id FROM project_environments WHERE id = $1`, envID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != v.ID {
		t.Fatalf("active pointer=%s want %s", active, v.ID)
	}
}
