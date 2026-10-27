package configversion

import (
	"context"
	"errors"
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
}

func NewService(repo *Repository, orgRepo *org.Repository, adapter ApplyAdapter) *Service {
	if adapter == nil {
		adapter = NewUnavailableApplyAdapter()
	}
	return &Service{repo: repo, orgRepo: orgRepo, adapter: adapter}
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
		if err := s.processJob(ctx, job); err != nil {
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) processJob(ctx context.Context, job ApplyJob) error {
	v, err := s.repo.GetVersion(ctx, job.EnvironmentID, job.ConfigurationVersionID)
	if err != nil {
		return err
	}
	if err := VerifyHash(v.ManifestCanonical, v.ManifestSHA256); err != nil {
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, "manifest_hash_mismatch", "stored manifest hash verification failed")
	}

	res, err := s.adapter.Apply(ctx, ApplyRequest{
		EnvironmentID:         job.EnvironmentID,
		VersionID:             v.ID,
		ManifestFormatVersion: v.ManifestFormatVersion,
		ManifestSHA256:        v.ManifestSHA256,
		ManifestCanonical:     v.ManifestCanonical,
		ApplyRequestID:        job.ApplyRequestID,
	})
	if err != nil {
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
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, code, msg)
	}
	if res.AppliedVersion != v.ID || res.AppliedSHA256 != v.ManifestSHA256 {
		return s.repo.MarkJobBlocked(ctx, job, job.FencingToken, "apply_ack_mismatch", "runtime acknowledgement did not match version and hash")
	}
	return s.repo.ActivateAfterApply(ctx, job, job.FencingToken, v.ManifestSHA256, job.ApplyRequestID)
}
