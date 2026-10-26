package org

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateOrganization(ctx context.Context, principal auth.Principal, in CreateOrgInput, requestID string) (Organization, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	name := strings.TrimSpace(in.DisplayName)
	if !slugPattern.MatchString(slug) {
		return Organization{}, ErrInvalidInput
	}
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return Organization{}, ErrInvalidInput
	}

	now := time.Now().UTC()
	org := Organization{
		ID:                      uuid.Must(uuid.NewV7()),
		Slug:                    slug,
		DisplayName:             name,
		Status:                  OrgStatusActive,
		CreatedByIdentityUserID: principal.UserID,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	meta, _ := json.Marshal(map[string]any{"slug": org.Slug})
	if err := s.repo.CreateOrganizationWithOwner(ctx, org, principal.UserID, principal.UserID, requestID, meta); err != nil {
		return Organization{}, err
	}
	return org, nil
}

func (s *Service) ListOrganizations(ctx context.Context, principal auth.Principal) ([]Organization, error) {
	return s.repo.ListOrganizationsForUser(ctx, principal.UserID)
}

func (s *Service) GetOrganization(ctx context.Context, principal auth.Principal, orgID uuid.UUID) (Organization, Membership, error) {
	m, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Organization{}, Membership{}, ErrNotFound
	}
	org, err := s.repo.GetOrganization(ctx, orgID)
	if err != nil {
		return Organization{}, Membership{}, err
	}
	return org, m, nil
}

func (s *Service) ArchiveOrganization(ctx context.Context, principal auth.Principal, orgID uuid.UUID, requestID string) error {
	return s.repo.ArchiveOrganizationAtomic(ctx, orgID, principal.UserID, requestID)
}

func (s *Service) ListMembers(ctx context.Context, principal auth.Principal, orgID uuid.UUID) ([]Membership, error) {
	m, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !m.Role.CanListMembers() {
		return nil, ErrForbidden
	}
	org, err := s.repo.GetOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if org.Status == OrgStatusArchived {
		return nil, ErrNotFound
	}
	return s.repo.ListMembers(ctx, orgID)
}

func (s *Service) ChangeMemberRole(ctx context.Context, principal auth.Principal, orgID, targetUserID uuid.UUID, newRole Role, requestID string) error {
	return s.repo.ChangeMemberRoleAtomic(ctx, orgID, principal.UserID, targetUserID, newRole, requestID)
}

func (s *Service) RemoveMember(ctx context.Context, principal auth.Principal, orgID, targetUserID uuid.UUID, requestID string) error {
	return s.repo.RemoveMemberAtomic(ctx, orgID, principal.UserID, targetUserID, requestID)
}
