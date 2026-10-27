package deployment

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupDeployEnv(t *testing.T) (context.Context, *pgxpool.Pool, *Service, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
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
	versionID := uuid.Must(uuid.NewV7())
	suffix := uuid.NewString()[:8]

	if _, err := pool.Exec(ctx, `
INSERT INTO organizations (id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1,$2,$2,'active',$3)`, orgID, "dep-org-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1,$2,'owner','active')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO projects (id, organization_id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1,$2,$3,$3,'active',$4)`, projectID, orgID, "dep-proj-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1,$2,'development','development','active')`, envID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO environment_configuration_versions (
  id, environment_id, version_number, schema_revision, pin_revision,
  manifest_format_version, manifest_canonical, manifest_sha256,
  compatibility, state, created_by_user_id
) VALUES (
  $1, $2, 1, 1, 1, 1, '{}',
  'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef',
  'compatible', 'active', $3
)`, versionID, envID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE project_environments SET active_configuration_version_id = $2 WHERE id = $1`, envID, versionID); err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewRepository(pool), NewUnavailableProvider(), nil)
	return ctx, pool, svc, userID, orgID, projectID, envID, versionID
}

func TestCreateUsesActiveConfigurationAndIdempotency(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	key := "idem-1-" + uuid.NewString()
	first, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.Deployment.ConfigurationVersionID != versionID {
		t.Fatal("expected fresh deployment bound to active version")
	}
	if first.Deployment.Status != StatusQueued {
		t.Fatalf("status=%s", first.Deployment.Status)
	}
	second, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.Deployment.ID != first.Deployment.ID {
		t.Fatal("idempotent create must reuse same deployment")
	}
}

func TestCreateRejectsForeignConfigurationVersion(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	otherEnv := uuid.Must(uuid.NewV7())
	otherVersion := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1,$2,'staging','staging','active')`, otherEnv, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO environment_configuration_versions (
  id, environment_id, version_number, schema_revision, pin_revision,
  manifest_format_version, manifest_canonical, manifest_sha256,
  compatibility, state, created_by_user_id
) VALUES (
  $1, $2, 1, 1, 1, 1, '{}',
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  'compatible', 'active', $3
)`, otherVersion, otherEnv, userID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		ConfigurationVersionID: &otherVersion,
		IdempotencyKey:         "cross-version-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrConfigMismatch) {
		t.Fatalf("error=%v; want config mismatch", err)
	}
}

func TestUnavailableProviderNeverReachesReady(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "fail-closed-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "test-worker-unavailable", 30*time.Second, 20); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("status=%s; want failed", got.Status)
	}
	if got.FailureCode != "provider_unavailable" {
		t.Fatalf("code=%s", got.FailureCode)
	}
	if got.ReadyAt != nil || got.ProviderURL != nil {
		t.Fatal("unavailable provider must not set ready evidence")
	}
}

func TestRecordingProviderReadyRequiresValidatedEvidence(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	rec := &RecordingProvider{
		Result: ProvisionResult{
			Outcome:              OutcomeSuccess,
			EnvironmentID:        envID,
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "test-readiness-probe",
			ReadinessObservedAt:  time.Now().UTC(),
			Host:                 "runtime.example.test",
			URL:                  "https://runtime.example.test",
			Region:               "local",
		},
	}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "ready-ok-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "test-worker-ready", 30*time.Second, 20); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusReady || got.ReadyAt == nil || got.ProviderHost == nil {
		t.Fatalf("expected ready with host evidence; status=%s", got.Status)
	}
}

func TestReadyRejectedOnEnvironmentMismatch(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	rec := &RecordingProvider{
		Result: ProvisionResult{
			Outcome:              OutcomeSuccess,
			EnvironmentID:        uuid.Must(uuid.NewV7()),
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "test-readiness-probe",
			ReadinessObservedAt:  time.Now().UTC(),
			Host:                 "evil.example",
		},
	}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "mismatch-env-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.ProcessOnce(ctx, "test-worker-mismatch", 30*time.Second, 20)
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == StatusReady {
		t.Fatal("mismatched environment evidence must not activate ready")
	}
}

func TestAuthorizationMatrixDeploy(t *testing.T) {
	ctx, pool, svc, _, orgID, projectID, envID, _ := setupDeployEnv(t)
	cases := []struct {
		role      string
		canDeploy bool
		canRead   bool
	}{
		{"admin", true, true},
		{"developer", false, true},
		{"viewer", false, true},
		{"billing", false, false},
	}
	for _, tc := range cases {
		uid := uuid.Must(uuid.NewV7())
		if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1,$2,$3,'active')`, orgID, uid, tc.role); err != nil {
			t.Fatal(err)
		}
		_, err := svc.Create(ctx, auth.Principal{UserID: uid}, orgID, projectID, envID, CreateInput{
			IdempotencyKey: "role-" + tc.role + "-" + uuid.NewString(),
		})
		if tc.canDeploy && err != nil {
			t.Fatalf("role %s deploy: %v", tc.role, err)
		}
		if !tc.canDeploy && !errors.Is(err, ErrForbidden) {
			t.Fatalf("role %s deploy error=%v", tc.role, err)
		}
		_, err = svc.List(ctx, auth.Principal{UserID: uid}, orgID, projectID, envID)
		if tc.canRead && err != nil {
			t.Fatalf("role %s read: %v", tc.role, err)
		}
		if !tc.canRead && !errors.Is(err, ErrForbidden) {
			t.Fatalf("role %s read error=%v", tc.role, err)
		}
	}
}

func TestConcurrentIdempotentCreate(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	key := "race-idem-" + uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan CreateResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
				IdempotencyKey: key,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent create: %v", err)
	}
	var id uuid.UUID
	count := 0
	for res := range results {
		count++
		if id == uuid.Nil {
			id = res.Deployment.ID
		} else if res.Deployment.ID != id {
			t.Fatal("concurrent idempotent create produced multiple deployments")
		}
	}
	if count != 8 {
		t.Fatalf("results=%d", count)
	}
}

func TestStaleWorkerCannotMarkReady(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	repo := NewRepository(pool)
	svc := NewService(repo, NewUnavailableProvider(), nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "stale-fence-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var d Deployment
	found := false
	for i := 0; i < 10 && !found; i++ {
		claimed, err := repo.Claim(ctx, "worker-a", 30*time.Second, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range claimed {
			if c.ID == created.Deployment.ID {
				d = c
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("created deployment was not claimed")
	}

	err = repo.MarkReady(ctx, d, "worker-b", ProvisionResult{
		Outcome:              OutcomeSuccess,
		DeploymentID:         d.ID,
		EnvironmentID:        d.EnvironmentID,
		VersionID:            d.ConfigurationVersionID,
		RequestID:            d.ProviderRequestID,
		ReadinessProbePassed: true,
		ReadinessProbeID:     "test-readiness-probe",
		ReadinessObservedAt:  time.Now().UTC(),
		Host:                 "x.example",
	})
	if !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("error=%v; want stale worker", err)
	}
}

var _ = org.RoleOwner

func TestExpiredLeaseCannotMarkReadyOrFailed(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	repo := NewRepository(pool)
	svc := NewService(repo, NewUnavailableProvider(), nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "expired-lease-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var d Deployment
	found := false
	for i := 0; i < 10 && !found; i++ {
		claimed, err := repo.Claim(ctx, "worker-lease", 30*time.Second, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range claimed {
			if c.ID == created.Deployment.ID {
				d = c
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("deployment not claimed")
	}

	_, err = pool.Exec(ctx, `
UPDATE deployments SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE id = $1`, d.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = repo.MarkReady(ctx, d, "worker-lease", ProvisionResult{
		Outcome:              OutcomeSuccess,
		DeploymentID:         d.ID,
		EnvironmentID:        envID,
		VersionID:            versionID,
		RequestID:            d.ProviderRequestID,
		ReadinessProbePassed: true,
		ReadinessProbeID:     "test-readiness-probe",
		ReadinessObservedAt:  time.Now().UTC(),
		Host:                 "host.example",
	})
	if !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("MarkReady after expiry: %v", err)
	}
	err = repo.MarkFailed(ctx, d, "worker-lease", "x", "y", true)
	if !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("MarkFailed after expiry: %v", err)
	}
}

func TestMaxAttemptsPreventsReclaim(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	repo := NewRepository(pool)
	svc := NewService(repo, NewUnavailableProvider(), nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "max-attempts-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, `
UPDATE deployments
SET attempt_count = max_attempts,
    status = 'provisioning',
    lease_owner = 'old-worker',
    lease_expires_at = clock_timestamp() - interval '1 second'
WHERE id = $1`, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.Claim(ctx, "worker-max", 30*time.Second, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range claimed {
		if c.ID == created.Deployment.ID {
			t.Fatal("must not claim deployment at max_attempts")
		}
	}

	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("status=%s; want failed after terminalize", got.Status)
	}
	if got.FailureCode != "max_attempts_exceeded" {
		t.Fatalf("code=%s", got.FailureCode)
	}
	if got.LeaseOwner != nil {
		t.Fatal("lease must be cleared on terminalize")
	}
}

func TestIdempotencyConflictOnDifferentFingerprint(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	key := "fp-conflict-" + uuid.NewString()
	first, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused {
		t.Fatal("first create must not be reused")
	}

	otherVersion := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
INSERT INTO environment_configuration_versions (
  id, environment_id, version_number, schema_revision, pin_revision,
  manifest_format_version, manifest_canonical, manifest_sha256,
  compatibility, state, created_by_user_id
) VALUES (
  $1, $2, 2, 1, 1, 1, '{}',
  'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  'compatible', 'queued', $3
)`, otherVersion, envID, userID); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey:         key,
		ConfigurationVersionID: &otherVersion,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v; want conflict on different fingerprint", err)
	}

	same, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !same.Reused || same.Deployment.ID != first.Deployment.ID {
		t.Fatal("same fingerprint must reuse")
	}

	explicitSame, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey:         key,
		ConfigurationVersionID: &versionID,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("omitted active vs explicit version must conflict: %v", err)
	}
	_ = explicitSame
}

func TestAmbiguousTimeoutDoesNotBecomeReady(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	rec := &RecordingProvider{
		Err: context.DeadlineExceeded,
	}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "timeout-amb-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.ProcessOnce(ctx, "worker-timeout", 30*time.Second, 20)
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == StatusReady {
		t.Fatal("timeout must not mark ready")
	}
	if got.Status != StatusFailed {
		t.Fatalf("status=%s; want failed terminal for ambiguous timeout", got.Status)
	}
	if got.FailureCode != "provider_ambiguous" {
		t.Fatalf("code=%s", got.FailureCode)
	}
	matched := 0
	for _, call := range rec.Calls {
		if call.DeploymentID == created.Deployment.ID {
			matched++
			if call.ProviderRequestID != created.Deployment.ProviderRequestID {
				t.Fatal("stable provider request id required")
			}
		}
	}
	if matched != 1 {
		t.Fatalf("provider calls for deployment=%d total=%d", matched, len(rec.Calls))
	}
}

func TestValidLeaseAllowsMarkReady(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	repo := NewRepository(pool)
	rec := &RecordingProvider{
		Result: ProvisionResult{
			Outcome:              OutcomeSuccess,
			EnvironmentID:        envID,
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "test-readiness-probe",
			ReadinessObservedAt:  time.Now().UTC(),
			Host:                 "ok.example",
			URL:                  "https://ok.example",
		},
	}
	svc := NewService(repo, rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "valid-lease-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-valid", 30*time.Second, 20); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusReady {
		t.Fatalf("status=%s", got.Status)
	}
}

type contextTimeoutProvider struct{}

func (p *contextTimeoutProvider) Name() ProviderName { return ProviderName("test-timeout") }

func (p *contextTimeoutProvider) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	<-ctx.Done()
	return ProvisionResult{}, ctx.Err()
}

func TestCancelledProviderContextPersistsAmbiguousOutcome(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	svc := NewService(NewRepository(pool), &contextTimeoutProvider{}, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "cancelled-provider-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-cancelled-provider", 100*time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "provider_ambiguous" {
		t.Fatalf("status=%s code=%s; want terminal ambiguous failure", got.Status, got.FailureCode)
	}
}

func TestReadyRequiresReadinessProbeEvidence(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	rec := &RecordingProvider{Result: ProvisionResult{
		Outcome: OutcomeSuccess, EnvironmentID: envID, VersionID: versionID,
		Host: "runtime.example.test", URL: "https://runtime.example.test",
	}}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "missing-probe-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-missing-probe", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "invalid_provider_evidence" {
		t.Fatalf("status=%s code=%s; want rejected readiness evidence", got.Status, got.FailureCode)
	}
	if got.ReadyAt != nil || got.ProviderURL != nil || got.ProviderHost != nil {
		t.Fatal("host and URL alone must not persist ready evidence")
	}
}

func TestProviderFailureIsSanitized(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	principal := auth.Principal{UserID: userID}
	sensitiveCode := "authorization Bearer secret-token"
	sensitiveMessage := "database password secret-password"
	rec := &RecordingProvider{Result: ProvisionResult{
		Outcome: OutcomeFailure, Code: sensitiveCode, Message: sensitiveMessage,
		EnvironmentID: envID, VersionID: versionID,
	}}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, principal, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "sanitize-provider-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-sanitize-provider", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, principal, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureCode != "provider_failure" || got.FailureMessage != "deployment provider failed" {
		t.Fatalf("unexpected public failure representation: code=%q message=%q", got.FailureCode, got.FailureMessage)
	}
	if strings.Contains(got.FailureCode, "secret-token") || strings.Contains(got.FailureMessage, "secret-password") {
		t.Fatal("provider diagnostics leaked into public failure fields")
	}
}

func TestClaimScansNullableReadinessEvidence(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "claim-columns-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := NewRepository(pool).Claim(ctx, "worker-claim-columns", 30*time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range claimed {
		if deployment.ID == created.Deployment.ID {
			if deployment.ReadinessProbeID != nil || deployment.ReadinessObservedAt != nil {
				t.Fatal("newly claimed deployment must not have readiness evidence")
			}
			return
		}
	}
	t.Fatal("created deployment was not returned by claim")
}

func TestReadyRejectsEvidenceForAnotherDeployment(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	rec := &RecordingProvider{Result: ProvisionResult{
		Outcome:              OutcomeSuccess,
		DeploymentID:         uuid.Must(uuid.NewV7()),
		EnvironmentID:        envID,
		VersionID:            versionID,
		ReadinessProbePassed: true,
		ReadinessProbeID:     "readiness-for-another-deployment",
		ReadinessObservedAt:  time.Now().UTC(),
		Host:                 "runtime.example.test",
		URL:                  "https://runtime.example.test",
	}}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "wrong-deployment-evidence-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-wrong-deployment", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "invalid_provider_evidence" {
		t.Fatalf("status=%s code=%s; want rejected evidence", got.Status, got.FailureCode)
	}
	if got.ReadyAt != nil || got.ProviderURL != nil || got.ProviderHost != nil {
		t.Fatal("mismatched deployment evidence must not persist ready target data")
	}
}

func TestReadyRejectsMismatchedHostAndURL(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	rec := &RecordingProvider{Result: ProvisionResult{
		Outcome:              OutcomeSuccess,
		EnvironmentID:        envID,
		VersionID:            versionID,
		ReadinessProbePassed: true,
		ReadinessProbeID:     "mismatched-target",
		ReadinessObservedAt:  time.Now().UTC(),
		Host:                 "evil.example",
		URL:                  "https://runtime.example.test",
	}}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "mismatched-target-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-mismatched-target", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "invalid_provider_evidence" {
		t.Fatalf("status=%s code=%s; want rejected target mismatch", got.Status, got.FailureCode)
	}
}

func TestReadyRejectsNonHTTPSURL(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, versionID := setupDeployEnv(t)
	rec := &RecordingProvider{Result: ProvisionResult{
		Outcome:              OutcomeSuccess,
		EnvironmentID:        envID,
		VersionID:            versionID,
		ReadinessProbePassed: true,
		ReadinessProbeID:     "non-https-target",
		ReadinessObservedAt:  time.Now().UTC(),
		Host:                 "runtime.example.test",
		URL:                  "http://runtime.example.test",
	}}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "non-https-target-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-non-https-target", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "invalid_provider_evidence" {
		t.Fatalf("status=%s code=%s; want rejected non-HTTPS target", got.Status, got.FailureCode)
	}
}

func TestProviderTransportErrorIsAmbiguous(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	rec := &RecordingProvider{Err: errors.New("connection reset after request may have been accepted")}
	svc := NewService(NewRepository(pool), rec, nil)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "ambiguous-transport-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-ambiguous-transport", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, created.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailureCode != "provider_ambiguous" {
		t.Fatalf("status=%s code=%s; want ambiguous terminal outcome", got.Status, got.FailureCode)
	}
}

func TestDatabaseRejectsReadyWithoutReadinessEvidence(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "database-ready-guard-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "UPDATE deployments SET status = 'ready' WHERE id = $1", created.Deployment.ID)
	if err == nil {
		t.Fatal("database must reject ready status without persisted readiness evidence")
	}
}

func TestExplicitQueuedConfigurationVersionCannotBeDeployed(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	candidateID := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `
INSERT INTO environment_configuration_versions (
  id, environment_id, version_number, schema_revision, pin_revision,
  manifest_format_version, manifest_canonical, manifest_sha256,
  compatibility, state, created_by_user_id
) VALUES (
  $1, $2, 2, 1, 1, 1, '{}',
  'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
  'compatible', 'queued', $3
)`, candidateID, envID, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "queued-config-" + uuid.NewString(),
		ConfigurationVersionID: &candidateID,
	})
	if !errors.Is(err, ErrVersionNotDeployable) {
		t.Fatalf("error=%v; want non-deployable configuration state", err)
	}
}

func TestWhitespaceOnlyIdempotencyKeyIsRejected(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	_, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: " \t\r\n ",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error=%v; want invalid input for whitespace-only idempotency key", err)
	}
}

func TestDeploymentOutcomeWritesAuditEvent(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	created, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "outcome-audit-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessOnce(ctx, "worker-audit", 30*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	var count int
	err = pool.QueryRow(ctx, `
SELECT count(*)
FROM control_plane_audit_events
WHERE event_type = 'deployment.failed'
  AND target_type = 'deployment'
  AND target_id = $1
  AND request_id = $2`, created.Deployment.ID.String(), created.Deployment.ProviderRequestID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("deployment failure audit rows=%d; want 1", count)
	}
}
func TestNoActiveConfigRejected(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID, _ := setupDeployEnv(t)
	if _, err := pool.Exec(ctx, `UPDATE project_environments SET active_configuration_version_id = NULL WHERE id = $1`, envID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(ctx, auth.Principal{UserID: userID}, orgID, projectID, envID, CreateInput{
		IdempotencyKey: "no-active-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrNoActiveConfig) {
		t.Fatalf("error=%v; want no active config", err)
	}
}
