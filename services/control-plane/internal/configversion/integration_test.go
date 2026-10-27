package configversion

import (
	"context"
	"errors"
	"log/slog"
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
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'cancelled', lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
WHERE environment_id = $1 AND state IN ('queued', 'leased')`, envID); err != nil {
			t.Errorf("clean up configuration apply jobs: %v", err)
		}
	})

	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	svc := NewService(repo, orgRepo, NewUnavailableApplyAdapter(), slog.Default())
	return ctx, pool, svc, userID, orgID, projectID, envID
}

func createConfirmedVersion(t *testing.T, ctx context.Context, svc *Service, principal auth.Principal, orgID, projectID, envID uuid.UUID, key string) Version {
	t.Helper()
	result, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedSchemaRevision: 0,
		ExpectedPinRevision:    0,
		IdempotencyKey:         key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version.State == StateAwaitingConfirmation {
		result.Version, err = svc.Confirm(ctx, principal, orgID, projectID, envID, ConfirmInput{
			VersionID:               result.Version.ID,
			ExpectedManifestSHA256:  result.Version.ManifestSHA256,
			ExpectedActiveVersionID: nil,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.repo.pool.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET next_attempt_at = now() - interval '1 second'
WHERE configuration_version_id = $1 AND state = 'queued'`, result.Version.ID); err != nil {
		t.Fatal(err)
	}
	return result.Version
}

func TestClaimJobsSerializesApplyPerEnvironment(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	v1 := createConfirmedVersion(t, ctx, svc, principal, orgID, projectID, envID, "claim-a-"+uuid.NewString())
	v2 := createConfirmedVersion(t, ctx, svc, principal, orgID, projectID, envID, "claim-b-"+uuid.NewString())

	type result struct {
		jobs []ApplyJob
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, owner := range []string{"claim-worker-a", "claim-worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			jobs, err := svc.repo.ClaimJobs(ctx, owner, time.Minute, 5)
			results <- result{jobs: jobs, err: err}
		}(owner)
	}
	wg.Wait()
	close(results)

	var claimed []ApplyJob
	for res := range results {
		if res.err != nil {
			t.Fatal(res.err)
		}
		for _, job := range res.jobs {
			if job.EnvironmentID == envID {
				claimed = append(claimed, job)
			}
		}
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs for one environment; want exactly 1", len(claimed))
	}
	if claimed[0].ConfigurationVersionID != v1.ID && claimed[0].ConfigurationVersionID != v2.ID {
		t.Fatalf("claimed unexpected version %s", claimed[0].ConfigurationVersionID)
	}
	if _, err := pool.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET state = 'cancelled', lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
WHERE environment_id = $1 AND state IN ('queued', 'leased')`, envID); err != nil {
		t.Fatal(err)
	}
}

func TestClaimJobsSkipsBusyEnvironmentAndClaimsReadyEnvironment(t *testing.T) {
	ctx, _, busyService, busyUser, busyOrg, busyProject, busyEnv := setupEnv(t)
	createConfirmedVersion(t, ctx, busyService, auth.Principal{UserID: busyUser}, busyOrg, busyProject, busyEnv, "busy-"+uuid.NewString())
	busyJobs, err := busyService.repo.ClaimJobs(ctx, "busy-owner", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(busyJobs) != 1 || busyJobs[0].EnvironmentID != busyEnv {
		t.Fatalf("precondition: expected lease on busy env %s, got %+v", busyEnv, busyJobs)
	}

	_, _, readyService, readyUser, readyOrg, readyProject, readyEnv := setupEnv(t)
	createConfirmedVersion(t, ctx, readyService, auth.Principal{UserID: readyUser}, readyOrg, readyProject, readyEnv, "ready-"+uuid.NewString())

	claimed, err := busyService.repo.ClaimJobs(ctx, "ready-owner", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs; want 1 from ready environment", len(claimed))
	}
	if claimed[0].EnvironmentID != readyEnv {
		t.Fatalf("claimed env %s; want ready env %s", claimed[0].EnvironmentID, readyEnv)
	}
}

func TestExpiredLeaseFencesPreviousWorker(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	version := createConfirmedVersion(t, ctx, svc, auth.Principal{UserID: userID}, orgID, projectID, envID, "lease-"+uuid.NewString())

	first, err := svc.repo.ClaimJobs(ctx, "lease-worker-a", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first claim returned %d jobs; want 1", len(first))
	}
	if _, err := pool.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET lease_expires_at = now() - interval '1 second'
WHERE id = $1`, first[0].ID); err != nil {
		t.Fatal(err)
	}

	second, err := svc.repo.ClaimJobs(ctx, "lease-worker-b", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("reclaim returned %d jobs; want 1", len(second))
	}
	if second[0].FencingToken != first[0].FencingToken+1 {
		t.Fatalf("fencing token=%d, want %d", second[0].FencingToken, first[0].FencingToken+1)
	}

	err = svc.repo.ActivateAfterApply(ctx, first[0], first[0].FencingToken, version.ManifestSHA256, first[0].ApplyRequestID)
	if !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("stale worker activation error=%v, want ErrStaleWorker", err)
	}
}

func TestRetryAttemptsExhaustToTerminalFailure(t *testing.T) {
	ctx, pool, svc, userID, orgID, projectID, envID := setupEnv(t)
	version := createConfirmedVersion(t, ctx, svc, auth.Principal{UserID: userID}, orgID, projectID, envID, "retry-"+uuid.NewString())

	for attempt := 1; attempt <= 5; attempt++ {
		jobs, err := svc.repo.ClaimJobs(ctx, "retry-worker", time.Minute, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 1 {
			t.Fatalf("attempt %d claimed %d jobs; want 1", attempt, len(jobs))
		}
		if jobs[0].AttemptCount != attempt {
			t.Fatalf("attempt count=%d, want %d", jobs[0].AttemptCount, attempt)
		}
		if err := svc.repo.ScheduleJobRetry(ctx, jobs[0], jobs[0].FencingToken, "transient", "temporary runtime error", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
UPDATE environment_configuration_apply_jobs
SET next_attempt_at = now() - interval '1 second'
WHERE configuration_version_id = $1 AND state = 'queued'`, version.ID); err != nil {
			t.Fatal(err)
		}
	}

	job, err := svc.repo.GetJobByVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotVersion, err := svc.repo.GetVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || gotVersion.State != StateFailed {
		t.Fatalf("job/version states = %s/%s; want failed/failed", job.State, gotVersion.State)
	}
	if job.AttemptCount != 5 || job.LastErrorCode != "max_attempts" {
		t.Fatalf("job attempts/error = %d/%q; want 5/max_attempts", job.AttemptCount, job.LastErrorCode)
	}
}

func TestApplyAcknowledgementMismatchBlocksWithoutActivation(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID := setupEnv(t)
	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	adapter := &RecordingApplyAdapter{
		Result: ApplyResult{
			Accepted:       true,
			EnvironmentID:  envID,
			AppliedVersion: uuid.Must(uuid.NewV7()),
			AppliedSHA256:  strings.Repeat("a", 64),
		},
	}
	svc := NewService(repo, orgRepo, adapter, slog.Default())
	version := createConfirmedVersion(t, ctx, svc, auth.Principal{UserID: userID}, orgID, projectID, envID, "ack-"+uuid.NewString())

	if _, err := svc.ProcessOnce(ctx, "ack-worker", time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	got, err := svc.repo.GetVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateBlocked {
		t.Fatalf("version state=%s; want blocked", got.State)
	}
	var active *uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT active_configuration_version_id
FROM project_environments
WHERE id = $1`, envID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatalf("invalid acknowledgement activated version %s", *active)
	}
}

func TestApplyNeedsReconciliationDoesNotActivate(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID := setupEnv(t)
	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	adapter := &RecordingApplyAdapter{}
	svc := NewService(repo, orgRepo, adapter, slog.Default())
	principal := auth.Principal{UserID: userID}
	version := createConfirmedVersion(t, ctx, svc, principal, orgID, projectID, envID, "recon-"+uuid.NewString())
	adapter.Result = ApplyResult{
		Accepted:                true,
		EnvironmentID:           envID,
		AppliedVersion:          version.ID,
		AppliedSHA256:           version.ManifestSHA256,
		ExpectedActiveVersionID: nil,
		NeedsReconciliation:     true,
	}

	if _, err := svc.ProcessOnce(ctx, "recon-worker", time.Minute, 5); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetVersion(ctx, principal, orgID, projectID, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateBlocked {
		t.Fatalf("version state=%s; want blocked", got.State)
	}
	var active *uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT active_configuration_version_id
FROM project_environments
WHERE id = $1`, envID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatalf("active pointer advanced to %s despite reconciliation requirement", *active)
	}
	job, err := svc.repo.GetJobByVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.LastErrorCode != "runtime_reconciliation_required" {
		t.Fatalf("job error code=%q; want runtime_reconciliation_required", job.LastErrorCode)
	}
}

func TestApplyMissingEnvironmentAckBlocks(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID := setupEnv(t)
	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	adapter := &RecordingApplyAdapter{}
	svc := NewService(repo, orgRepo, adapter, slog.Default())
	principal := auth.Principal{UserID: userID}
	version := createConfirmedVersion(t, ctx, svc, principal, orgID, projectID, envID, "noenv-"+uuid.NewString())
	adapter.Result = ApplyResult{
		Accepted:                true,
		AppliedVersion:          version.ID,
		AppliedSHA256:           version.ManifestSHA256,
		ExpectedActiveVersionID: nil,
	}

	if _, err := svc.ProcessOnce(ctx, "noenv-worker", time.Minute, 5); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetVersion(ctx, principal, orgID, projectID, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateBlocked {
		t.Fatalf("version state=%s; want blocked", got.State)
	}
	job, err := svc.repo.GetJobByVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.LastErrorCode != "apply_ack_mismatch" {
		t.Fatalf("job error code=%q; want apply_ack_mismatch", job.LastErrorCode)
	}
	_ = pool
}

func TestConfigVersionSuccessfulApplyActivatesAndSetsActivePointer(t *testing.T) {
	ctx, pool, _, userID, orgID, projectID, envID := setupEnv(t)
	orgRepo := org.NewRepository(pool)
	repo := NewRepository(pool)
	adapter := &RecordingApplyAdapter{}
	svc := NewService(repo, orgRepo, adapter, slog.Default())
	principal := auth.Principal{UserID: userID}

	version := createConfirmedVersion(t, ctx, svc, principal, orgID, projectID, envID, "activate-"+uuid.NewString())
	adapter.Result = ApplyResult{
		Accepted:                true,
		EnvironmentID:           envID,
		AppliedVersion:          version.ID,
		AppliedSHA256:           version.ManifestSHA256,
		ExpectedActiveVersionID: nil,
	}

	n, err := svc.ProcessOnce(ctx, "activate-worker", time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("processed=%d; want at least 1", n)
	}

	got, err := svc.GetVersion(ctx, principal, orgID, projectID, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateActive {
		t.Fatalf("version state=%s; want active", got.State)
	}
	if got.ActivatedAt == nil {
		t.Fatal("activated_at is nil")
	}

	var active uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT active_configuration_version_id
FROM project_environments
WHERE id = $1`, envID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != version.ID {
		t.Fatalf("active_configuration_version_id=%s; want %s", active, version.ID)
	}

	job, err := svc.repo.GetJobByVersion(ctx, envID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobSucceeded {
		t.Fatalf("job state=%s; want succeeded", job.State)
	}
	if len(adapter.Calls) != 1 {
		t.Fatalf("adapter calls=%d; want 1", len(adapter.Calls))
	}
	call := adapter.Calls[0]
	if call.EnvironmentID != envID || call.VersionID != version.ID || call.ManifestSHA256 != version.ManifestSHA256 {
		t.Fatalf("adapter request mismatch: %+v", call)
	}
}

func TestConfigVersionIdempotentCandidateReuse(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}
	key := "idem-" + uuid.NewString()
	in := CreateCandidateInput{
		ExpectedSchemaRevision: 0,
		ExpectedPinRevision:    0,
		IdempotencyKey:         key,
	}

	first, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused {
		t.Fatal("first create should not be reused")
	}

	second, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Fatal("second create with same key should be reused")
	}
	if second.Version.ID != first.Version.ID {
		t.Fatalf("version ids differ: %s vs %s", first.Version.ID, second.Version.ID)
	}
	if second.Version.VersionNumber != first.Version.VersionNumber {
		t.Fatalf("version numbers differ: %d vs %d", first.Version.VersionNumber, second.Version.VersionNumber)
	}
	if second.Version.ManifestSHA256 != first.Version.ManifestSHA256 {
		t.Fatal("manifest hashes differ on idempotent reuse")
	}
}

func TestConfigVersionStaleRevisionRejected(t *testing.T) {
	ctx, _, svc, userID, orgID, projectID, envID := setupEnv(t)
	principal := auth.Principal{UserID: userID}

	_, err := svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedSchemaRevision: 99,
		ExpectedPinRevision:    0,
		IdempotencyKey:         "stale-schema-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrConflictRevision) {
		t.Fatalf("stale schema revision error=%v; want ErrConflictRevision", err)
	}

	_, err = svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedSchemaRevision: 0,
		ExpectedPinRevision:    7,
		IdempotencyKey:         "stale-pin-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrConflictRevision) {
		t.Fatalf("stale pin revision error=%v; want ErrConflictRevision", err)
	}

	bogusActive := uuid.Must(uuid.NewV7())
	_, err = svc.CreateCandidate(ctx, principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedActiveVersionID: &bogusActive,
		ExpectedSchemaRevision:  0,
		ExpectedPinRevision:     0,
		IdempotencyKey:          "stale-active-" + uuid.NewString(),
	})
	if !errors.Is(err, ErrConflictRevision) {
		t.Fatalf("stale expected active error=%v; want ErrConflictRevision", err)
	}
}
