package apikey

import (
	"context"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/requestid"
	"github.com/google/uuid"
)

type Service struct {
	repo    *Repository
	orgRepo *org.Repository
}

func NewService(repo *Repository, orgRepo *org.Repository) *Service {
	return &Service{repo: repo, orgRepo: orgRepo}
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

func (s *Service) Create(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, in CreateInput) (IssuedKey, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return IssuedKey{}, err
	}
	if !m.Role.CanManageAPIKeys() {
		return IssuedKey{}, ErrForbidden
	}
	return s.repo.Create(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), in, requestid.FromContext(ctx))
}

func (s *Service) List(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]KeyRecord, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanManageAPIKeys() {
		return nil, ErrForbidden
	}
	return s.repo.List(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID))
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, orgID, projectID, envID, keyID uuid.UUID) (KeyRecord, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return KeyRecord{}, err
	}
	if !m.Role.CanManageAPIKeys() {
		return KeyRecord{}, ErrForbidden
	}
	return s.repo.Get(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), keyID)
}

func (s *Service) Revoke(ctx context.Context, principal auth.Principal, orgID, projectID, envID, keyID uuid.UUID) (KeyRecord, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return KeyRecord{}, err
	}
	if !m.Role.CanManageAPIKeys() {
		return KeyRecord{}, ErrForbidden
	}
	return s.repo.Revoke(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), keyID, requestid.FromContext(ctx))
}

func (s *Service) Rotate(ctx context.Context, principal auth.Principal, orgID, projectID, envID, keyID uuid.UUID) (IssuedKey, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return IssuedKey{}, err
	}
	if !m.Role.CanManageAPIKeys() {
		return IssuedKey{}, ErrForbidden
	}
	return s.repo.Rotate(ctx, s.mutationAuth(orgID, projectID, envID, principal.UserID), keyID, requestid.FromContext(ctx))
}

func (s *Service) Verify(ctx context.Context, fullKey string) (ResolveResult, error) {
	return s.repo.Verify(ctx, fullKey)
}
