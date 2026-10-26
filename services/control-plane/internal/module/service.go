package module

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

type Service struct {
	repo        *Repository
	orgRepo     *org.Repository
	projectRepo *project.Repository
}

func NewService(repo *Repository, orgRepo *org.Repository, projectRepo *project.Repository) *Service {
	return &Service{repo: repo, orgRepo: orgRepo, projectRepo: projectRepo}
}

func (s *Service) ListModules(ctx context.Context) ([]Module, error) {
	return s.repo.ListModules(ctx)
}

func (s *Service) ListReleases(ctx context.Context, moduleSlug string) ([]Release, error) {
	return s.repo.ListReleases(ctx, moduleSlug)
}

func (s *Service) ResolveSelections(ctx context.Context, selections []Selection) (ResolutionPlan, error) {
	modules, err := s.repo.ListModules(ctx)
	if err != nil {
		return ResolutionPlan{}, err
	}
	releases, err := s.repo.ListReleases(ctx, "")
	if err != nil {
		return ResolutionPlan{}, err
	}
	return Resolve(modules, releases, selections)
}

func (s *Service) authorizeEnv(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, write bool) error {
	m, err := s.orgRepo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return ErrNotFound
	}
	if write {
		if !m.Role.CanUpdateProject() {
			return ErrForbidden
		}
	} else {
		if !m.Role.CanReadProject() {
			return ErrForbidden
		}
	}
	if _, err := s.projectRepo.GetProject(ctx, orgID, projectID); err != nil {
		return ErrNotFound
	}
	if _, err := s.projectRepo.GetEnvironment(ctx, projectID, envID); err != nil {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]Pin, error) {
	if err := s.authorizeEnv(ctx, principal, orgID, projectID, envID, false); err != nil {
		return nil, err
	}
	return s.repo.ListPins(ctx, envID)
}

func (s *Service) SetPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, selections []Selection) (ResolutionPlan, error) {
	if err := s.authorizeEnv(ctx, principal, orgID, projectID, envID, true); err != nil {
		return ResolutionPlan{}, err
	}
	plan, err := s.ResolveSelections(ctx, selections)
	if err != nil {
		return ResolutionPlan{}, err
	}
	pins := make([]Pin, 0, len(plan.Releases))
	for _, rr := range plan.Releases {
		id, err := uuid.Parse(rr.ReleaseID)
		if err != nil {
			return ResolutionPlan{}, err
		}
		pins = append(pins, Pin{
			EnvironmentID: envID,
			ModuleSlug:    rr.ModuleSlug,
			ReleaseID:     id,
			Version:       rr.Version,
		})
	}
	if err := s.repo.ReplacePins(ctx, envID, pins); err != nil {
		return ResolutionPlan{}, err
	}
	return plan, nil
}
