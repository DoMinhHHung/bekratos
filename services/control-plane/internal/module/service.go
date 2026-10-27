package module

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrForbidden     = errors.New("forbidden")
	ErrInactive      = errors.New("resource inactive")
	ErrInvalidInput  = errors.New("invalid input")
	ErrConflictState = errors.New("conflict")
)

const (
	platformRuntimeVersion = "1.0.0"
	platformAPIContract    = "1"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ListModules(ctx context.Context) ([]Module, error) {
	return s.repo.ListModules(ctx)
}

func (s *Service) ListReleases(ctx context.Context, moduleSlug string) ([]Release, error) {
	return s.repo.ListReleases(ctx, moduleSlug)
}

func productionResolveOptions() ResolveOptions {
	return ResolveOptions{
		RuntimeVersion: platformRuntimeVersion,
		APIContract:    platformAPIContract,
	}
}

func (s *Service) ResolveSelections(ctx context.Context, selections []Selection) (ResolutionPlan, error) {
	modules, err := s.repo.ListModules(ctx)
	if err != nil {
		return ResolutionPlan{}, err
	}
	releases, err := s.repo.ListPublishedReleases(ctx, "")
	if err != nil {
		return ResolutionPlan{}, err
	}
	return ResolveWithOptions(modules, releases, selections, productionResolveOptions())
}

func (s *Service) ListPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]Pin, int64, error) {
	return s.repo.ListPinsAuthorized(ctx, orgID, projectID, envID, principal.UserID)
}

func (s *Service) SetPins(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, selections []Selection, expectedRevision *int64, requestID string) (ResolutionPlan, error) {
	if expectedRevision == nil {
		return ResolutionPlan{}, ErrInvalidInput
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
	newRev, err := s.repo.ReplacePinsAuthorized(ctx, orgID, projectID, envID, pins, expectedRevision, actor, requestID)
	if err != nil {
		if errors.Is(err, ErrConflictRevision) {
			return ResolutionPlan{}, ErrConflictState
		}
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInactive) || errors.Is(err, ErrNotFound) {
			return ResolutionPlan{}, err
		}
		return ResolutionPlan{}, err
	}
	plan.Revision = newRev
	return plan, nil
}
