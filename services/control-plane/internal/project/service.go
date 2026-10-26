package project

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)

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

func (s *Service) requireActiveOrg(ctx context.Context, orgID uuid.UUID) error {
	o, err := s.orgRepo.GetOrganization(ctx, orgID)
	if err != nil {
		return ErrNotFound
	}
	if o.Status != org.OrgStatusActive {
		return ErrForbidden
	}
	return nil
}

func (s *Service) CreateProject(ctx context.Context, principal auth.Principal, orgID uuid.UUID, in CreateProjectInput) (Project, Environment, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Project{}, Environment{}, err
	}
	if !m.Role.CanCreateProject() {
		return Project{}, Environment{}, ErrForbidden
	}
	if err := s.requireActiveOrg(ctx, orgID); err != nil {
		return Project{}, Environment{}, err
	}

	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	name := strings.TrimSpace(in.DisplayName)
	desc := strings.TrimSpace(in.Description)
	if !slugPattern.MatchString(slug) || name == "" || utf8.RuneCountInString(name) > 120 {
		return Project{}, Environment{}, ErrInvalidInput
	}
	if utf8.RuneCountInString(desc) > 2000 {
		return Project{}, Environment{}, ErrInvalidInput
	}

	now := time.Now().UTC()
	p := Project{
		ID:                      uuid.Must(uuid.NewV7()),
		OrganizationID:          orgID,
		Slug:                    slug,
		DisplayName:             name,
		Description:             desc,
		Status:                  StatusActive,
		CreatedByIdentityUserID: principal.UserID,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	env := Environment{
		ID:        uuid.Must(uuid.NewV7()),
		ProjectID: p.ID,
		Slug:      "development",
		Kind:      KindDevelopment,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateProjectWithDevEnv(ctx, p, env); err != nil {
		return Project{}, Environment{}, err
	}
	return p, env, nil
}

func (s *Service) ListProjects(ctx context.Context, principal auth.Principal, orgID uuid.UUID) ([]Project, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanReadProject() {
		return nil, ErrForbidden
	}
	return s.repo.ListProjectsByOrg(ctx, orgID)
}

func (s *Service) GetProject(ctx context.Context, principal auth.Principal, orgID, projectID uuid.UUID) (Project, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Project{}, err
	}
	if !m.Role.CanReadProject() {
		return Project{}, ErrForbidden
	}
	return s.repo.GetProject(ctx, orgID, projectID)
}

func (s *Service) UpdateProject(ctx context.Context, principal auth.Principal, orgID, projectID uuid.UUID, in UpdateProjectInput) (Project, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Project{}, err
	}
	if !m.Role.CanUpdateProject() {
		return Project{}, ErrForbidden
	}
	if err := s.requireActiveOrg(ctx, orgID); err != nil {
		return Project{}, err
	}
	if in.DisplayName != nil {
		n := strings.TrimSpace(*in.DisplayName)
		if n == "" || utf8.RuneCountInString(n) > 120 {
			return Project{}, ErrInvalidInput
		}
		in.DisplayName = &n
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d) > 2000 {
			return Project{}, ErrInvalidInput
		}
		in.Description = &d
	}
	return s.repo.UpdateProject(ctx, orgID, projectID, in.DisplayName, in.Description)
}

func (s *Service) ArchiveProject(ctx context.Context, principal auth.Principal, orgID, projectID uuid.UUID) error {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return err
	}
	if !m.Role.CanArchiveProject() {
		return ErrForbidden
	}
	if err := s.requireActiveOrg(ctx, orgID); err != nil {
		return err
	}
	return s.repo.ArchiveProject(ctx, orgID, projectID)
}

func (s *Service) CreateEnvironment(ctx context.Context, principal auth.Principal, orgID, projectID uuid.UUID, in CreateEnvironmentInput) (Environment, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Environment{}, err
	}
	if !m.Role.CanCreateEnvironment() {
		return Environment{}, ErrForbidden
	}
	if err := s.requireActiveOrg(ctx, orgID); err != nil {
		return Environment{}, err
	}
	proj, err := s.repo.GetProject(ctx, orgID, projectID)
	if err != nil {
		return Environment{}, err
	}
	if proj.Status != StatusActive {
		return Environment{}, ErrForbidden
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugPattern.MatchString(slug) {
		return Environment{}, ErrInvalidInput
	}
	switch in.Kind {
	case KindDevelopment, KindStaging, KindProduction, KindCustom:
	default:
		return Environment{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	env := Environment{
		ID:        uuid.Must(uuid.NewV7()),
		ProjectID: projectID,
		Slug:      slug,
		Kind:      in.Kind,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateEnvironment(ctx, env); err != nil {
		return Environment{}, err
	}
	return env, nil
}

func (s *Service) ListEnvironments(ctx context.Context, principal auth.Principal, orgID, projectID uuid.UUID) ([]Environment, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanReadEnvironment() {
		return nil, ErrForbidden
	}
	if _, err := s.repo.GetProject(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	return s.repo.ListEnvironments(ctx, projectID)
}

func (s *Service) GetEnvironment(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) (Environment, error) {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return Environment{}, err
	}
	if !m.Role.CanReadEnvironment() {
		return Environment{}, ErrForbidden
	}
	if _, err := s.repo.GetProject(ctx, orgID, projectID); err != nil {
		return Environment{}, err
	}
	return s.repo.GetEnvironment(ctx, projectID, envID)
}

func (s *Service) ArchiveEnvironment(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) error {
	m, err := s.requireMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return err
	}
	if !m.Role.CanArchiveEnvironment() {
		return ErrForbidden
	}
	if err := s.requireActiveOrg(ctx, orgID); err != nil {
		return err
	}
	if _, err := s.repo.GetProject(ctx, orgID, projectID); err != nil {
		return err
	}
	return s.repo.ArchiveEnvironment(ctx, projectID, envID)
}
