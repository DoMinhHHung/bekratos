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

func (s *Service) CreateOrganization(ctx context.Context, principal auth.Principal, in CreateOrgInput) (Organization, error) {
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
	if err := s.repo.CreateOrganizationWithOwner(ctx, org, principal.UserID); err != nil {
		return Organization{}, err
	}
	_ = s.audit(ctx, &principal.UserID, &org.ID, "organization.created", "organization", org.ID.String(), map[string]any{
		"slug": org.Slug,
	})
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

func (s *Service) ArchiveOrganization(ctx context.Context, principal auth.Principal, orgID uuid.UUID) error {
	m, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return ErrNotFound
	}
	if !m.Role.CanArchiveOrg() {
		return ErrForbidden
	}
	if err := s.repo.ArchiveOrganization(ctx, orgID); err != nil {
		return err
	}
	_ = s.audit(ctx, &principal.UserID, &orgID, "organization.archived", "organization", orgID.String(), nil)
	return nil
}

func (s *Service) ListMembers(ctx context.Context, principal auth.Principal, orgID uuid.UUID) ([]Membership, error) {
	m, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !m.Role.CanListMembers() {
		return nil, ErrForbidden
	}
	return s.repo.ListMembers(ctx, orgID)
}

func (s *Service) ChangeMemberRole(ctx context.Context, principal auth.Principal, orgID, targetUserID uuid.UUID, newRole Role) error {
	actor, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return ErrNotFound
	}
	if !actor.Role.CanManageMembers() {
		return ErrForbidden
	}
	if !actor.Role.CanAssignRole(newRole) {
		return ErrForbidden
	}

	target, err := s.repo.GetActiveMembership(ctx, orgID, targetUserID)
	if err != nil {
		return ErrNotFound
	}
	if !actor.Role.CanModifyMember(target.Role) {
		return ErrForbidden
	}

	if err := s.repo.ChangeMemberRole(ctx, orgID, targetUserID, newRole); err != nil {
		return err
	}
	_ = s.audit(ctx, &principal.UserID, &orgID, "membership.role_changed", "membership", targetUserID.String(), map[string]any{
		"from": string(target.Role),
		"to":   string(newRole),
	})
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, principal auth.Principal, orgID, targetUserID uuid.UUID) error {
	actor, err := s.repo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return ErrNotFound
	}
	if !actor.Role.CanManageMembers() {
		return ErrForbidden
	}

	target, err := s.repo.GetActiveMembership(ctx, orgID, targetUserID)
	if err != nil {
		return ErrNotFound
	}
	if !actor.Role.CanModifyMember(target.Role) {
		return ErrForbidden
	}

	if err := s.repo.RemoveMember(ctx, orgID, targetUserID); err != nil {
		return err
	}
	_ = s.audit(ctx, &principal.UserID, &orgID, "membership.removed", "membership", targetUserID.String(), map[string]any{
		"role": string(target.Role),
	})
	return nil
}

func (s *Service) audit(ctx context.Context, actor, orgID *uuid.UUID, eventType, targetType, targetID string, meta map[string]any) error {
	var raw []byte
	if meta != nil {
		raw, _ = json.Marshal(meta)
	}
	return s.repo.InsertAudit(ctx, uuid.Must(uuid.NewV7()), actor, orgID, eventType, targetType, targetID, "", raw)
}
