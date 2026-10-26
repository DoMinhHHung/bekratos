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
	ErrNotFound      = errors.New("not found")
	ErrForbidden     = errors.New("forbidden")
	ErrInactive      = errors.New("resource inactive")
	ErrConflictState = errors.New("conflict")
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

func (s *Service) authorizeEnv(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, write bool) (project.Project, project.Environment, error) {
	m, err := s.orgRepo.GetActiveMembership(ctx, orgID, principal.UserID)
	if err != nil {
		return project.Project{}, project.Environment{}, ErrNotFound
	}
	if write {
		if !m.Role.CanUpdateProject() {
			return project.Project{}, project.Environment{}, ErrForbidden
		}
	} else {
		if !m.Role.CanReadProject() {
			return project.Project{}, project.Environment{}, ErrForbidden
		}
	}
	p, err := s.projectRepo.GetProject(ctx, orgID, projectID)
	if err != nil {
		return project.Project{}, project.Environment{}, ErrNotFound
	}
	if write && p.Status != project.StatusActive {
		return project.Project{}, project.Environment{}, ErrInactive
	}
	e, err := s.projectRepo.GetEnvironment(ctx, projectID, envID)
	if err != nil {
		return project.Project{}, project.Environment{}, ErrNotFound
	}
	if write && e.Status != project.StatusActive {
		return project.Project{}, project.Environment{}, ErrInactive
	}
	return p, e, nil
}

func (s *Service) ListPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]Pin, int64, error) {
	if _, _, err := s.authorizeEnv(ctx, principal, orgID, projectID, envID, false); err != nil {
		return nil, 0, err
	}
	return s.repo.ListPins(ctx, envID)
}

func (s *Service) SetPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, selections []Selection, expectedRevision *int64) (ResolutionPlan, error) {
	if _, _, err := s.authorizeEnv(ctx, principal, orgID, projectID, envID, true); err != nil {
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
	actor := principal.UserID
	newRev, err := s.repo.ReplacePins(ctx, envID, pins, expectedRevision, &actor, &orgID)
	if err != nil {
		if errors.Is(err, ErrConflictRevision) {
			return ResolutionPlan{}, ErrConflictState
		}
		return ResolutionPlan{}, err
	}
	plan.Revision = newRev
	return plan, nil
}
