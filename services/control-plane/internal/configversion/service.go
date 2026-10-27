package configversion

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/requestid"
	"github.com/google/uuid"
)

type Service struct {
	repo    *Repository
	orgRepo *org.Repository
	adapter ApplyAdapter
	log     *slog.Logger
}

func NewService(repo *Repository, orgRepo *org.Repository, adapter ApplyAdapter, log *slog.Logger) *Service {
	if adapter == nil {
		adapter = NewUnavailableApplyAdapter()
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, orgRepo: orgRepo, adapter: adapter, log: log}
}

func (s *Service) requireMembership(ctx context.Context, orgID, userID uuid.UUID) (org.Membership, error) {
	m, err := s.orgRepo.GetActiveMembership(ctx, orgID, userID)
	if err != nil {
		return org.Membership{}, ErrNotFound
	}
	o, err := s.orgRepo.GetOrganization(ctx, orgID)
	if err != nil {
		return org.Membership{}, ErrNotFound
	}
	if o.Status == org.OrgStatusArchived {
		return org.Membership{}, ErrNotFound
	}
	return m, nil
}

func (s *Service) mutationAuth(orgID, projectID, envID, actorID uuid.UUID) MutationAuth {
	return MutationAuth{OrgID: orgID, ProjectID: projectID, EnvID: envID, ActorID: actorID}
}

func (s *Service) CreateCandidate(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, in CreateCandidateInput) (CreateResult, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return CreateResult{}, err
	}
	if !m.Role.CanMutateSchema() {
		return CreateResult{}, ErrForbidden
	}
	return s.repo.CreateCandidate(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), in, requestid.FromContext(ctx))
}

func (s *Service) ListVersions(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]Version, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanReadSchema() {
		return nil, ErrForbidden
	}
	if _, _, _, err := s.repo.GetEnvironmentStatuses(ctx, orgID, projectID, envID); err != nil {
		return nil, err
	}
	return s.repo.ListVersions(ctx, envID, 50)
}

func (s *Service) GetVersion(ctx context.Context, principal auth.Principal, orgID, projectID, envID, versionID uuid.UUID) (Version, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Version{}, err
	}
	if !m.Role.CanReadSchema() {
		return Version{}, ErrForbidden
	}
	if _, _, _, err := s.repo.GetEnvironmentStatuses(ctx, orgID, projectID, envID); err != nil {
		return Version{}, err
	}
	return s.repo.GetVersion(ctx, envID, versionID)
}

func (s *Service) GetJob(ctx context.Context, principal auth.Principal, orgID, projectID, envID, versionID uuid.UUID) (ApplyJob, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return ApplyJob{}, err
	}
	if !m.Role.CanReadSchema() {
		return ApplyJob{}, ErrForbidden
	}
	if _, _, _, err := s.repo.GetEnvironmentStatuses(ctx, orgID, projectID, envID); err != nil {
		return ApplyJob{}, err
	}
	return s.repo.GetJobByVersion(ctx, envID, versionID)
}

func (s *Service) Confirm(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, in ConfirmInput) (Version, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Version{}, err
	}
	if !m.Role.CanMutateSchema() {
		return Version{}, ErrForbidden
	}
	if in.ExpectedManifestSHA256 == "" {
		return Version{}, ErrInvalidInput
	}
	return s.repo.ConfirmCandidate(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), in, requestid.FromContext(ctx))
}

func (s *Service) ProcessOnce(ctx context.Context, workerID string, lease time.Duration, limit int) (int, error) {
	if workerID == "" {
		workerID = "worker"
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if limit <= 0 {
		limit = 1
	}
	jobs, err := s.repo.ClaimJobs(ctx, workerID, lease, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if err := s.processJob(ctx, job, lease); err != nil {
			s.log.Warn("config apply job finished with error",
				"job_id", job.ID.String(),
				"environment_id", job.EnvironmentID.String(),
				"version_id", job.ConfigurationVersionID.String(),
				"error", err.Error(),
			)
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) processJob(ctx context.Context, job ApplyJob, lease time.Duration) error {
	v, err := s.repo.GetVersion(ctx, job.EnvironmentID, job.ConfigurationVersionID)
	if err != nil {
		if terminalErr := s.repo.ReleaseJobTerminal(ctx, job, job.FencingToken, JobFailed, "version_missing", "configuration version missing"); terminalErr != nil {
			s.log.Warn("failed to terminalize job after missing version",
				"job_id", job.ID.String(),
				"error", terminalErr.Error(),
			)
		}
		return err
	}
	if err := VerifyHash(v.ManifestCanonical, v.ManifestSHA256); err != nil {
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, "manifest_hash_mismatch", "stored manifest hash verification failed")
	}

	res, err := s.adapter.Apply(ctx, ApplyRequest{
		EnvironmentID:           job.EnvironmentID,
		VersionID:               v.ID,
		ManifestFormatVersion:   v.ManifestFormatVersion,
		ManifestSHA256:          v.ManifestSHA256,
		ManifestCanonical:       v.ManifestCanonical,
		ApplyRequestID:          job.ApplyRequestID,
		ExpectedActiveVersionID: v.ExpectedActiveVersionID,
	})
	if err != nil {
		var retryable RetryableApplyError
		if errors.As(err, &retryable) {
			return s.repo.ScheduleJobRetry(ctx, job, job.FencingToken, retryable.Code, retryable.Message, lease)
		}
		var ambiguous AmbiguousApplyError
		if errors.As(err, &ambiguous) {
			return s.repo.MarkJobNeedsReconciliation(ctx, job, job.FencingToken, "runtime_apply_ambiguous",
				"runtime apply outcome is unknown; lookup by apply_request_id is required")
		}
		code := "runtime_error"
		msg := "runtime apply failed"
		if errors.Is(err, ErrRuntimeUnavailable) {
			code = "runtime_unavailable"
			msg = "runtime configuration apply contract is not configured"
		}
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, code, msg)
	}
	if !res.Accepted {
		code := res.Code
		if code == "" {
			code = "apply_rejected"
		}
		msg := res.Message
		if msg == "" {
			msg = "runtime rejected configuration apply"
		}
		if code == "stale_base" {
			return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, code, msg)
		}
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, code, msg)
	}
	if res.NeedsReconciliation {
		return s.repo.MarkJobNeedsReconciliation(ctx, job, job.FencingToken, "runtime_reconciliation_required",
			"runtime accepted apply but reported reconciliation required; active pointer not advanced")
	}
	if res.EnvironmentID != job.EnvironmentID ||
		res.AppliedVersion != v.ID ||
		res.AppliedSHA256 != v.ManifestSHA256 ||
		!uuidPtrEqual(res.ExpectedActiveVersionID, v.ExpectedActiveVersionID) {
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, "apply_ack_mismatch", "runtime acknowledgement did not match version hash environment or base")
	}
	err = s.repo.ActivateAfterApply(ctx, job, job.FencingToken, v.ManifestSHA256, job.ApplyRequestID)
	if err != nil {
		code := "activation_failed"
		message := "runtime accepted apply but control-plane activation transaction failed; manual reconciliation required"
		if errors.Is(err, ErrConflictRevision) || errors.Is(err, ErrStaleWorker) {
			code = "activation_conflict"
			message = "runtime accepted apply but control-plane activation failed; manual reconciliation required"
		}
		if reconciliationErr := s.repo.MarkJobNeedsReconciliation(ctx, job, job.FencingToken, code, message); reconciliationErr != nil {
			s.log.Error("failed to mark job for reconciliation after activation failure",
				"job_id", job.ID.String(),
				"version_id", job.ConfigurationVersionID.String(),
				"error", reconciliationErr.Error(),
			)
		}
		return err
	}
	return nil
}
