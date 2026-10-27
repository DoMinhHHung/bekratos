package deployment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/requestid"
	"github.com/google/uuid"
)

type Service struct {
	repo     *Repository
	provider Provider
	log      *slog.Logger
}

func NewService(repo *Repository, provider Provider, log *slog.Logger) *Service {
	if provider == nil {
		provider = NewUnavailableProvider()
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, provider: provider, log: log}
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID, in CreateInput) (CreateResult, error) {
	return s.repo.Create(ctx, MutationAuth{
		OrgID: orgID, ProjectID: projectID, EnvID: envID, ActorID: principal.UserID,
	}, in, requestid.FromContext(ctx))
}

func (s *Service) List(ctx context.Context, principal auth.Principal, orgID, projectID, envID uuid.UUID) ([]Deployment, error) {
	return s.repo.List(ctx, MutationAuth{
		OrgID: orgID, ProjectID: projectID, EnvID: envID, ActorID: principal.UserID,
	})
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, orgID, projectID, envID, deploymentID uuid.UUID) (Deployment, error) {
	return s.repo.Get(ctx, MutationAuth{
		OrgID: orgID, ProjectID: projectID, EnvID: envID, ActorID: principal.UserID,
	}, deploymentID)
}

func (s *Service) ProcessOnce(ctx context.Context, workerID string, lease time.Duration, batch int) (int, error) {
	claimed, err := s.repo.Claim(ctx, workerID, lease, batch)
	if err != nil {
		return 0, err
	}
	for _, d := range claimed {
		if err := s.processOne(ctx, workerID, d); err != nil {
			s.log.Warn("deployment process failed", "deployment_id", d.ID.String(), "error", err.Error())
		}
	}
	return len(claimed), nil
}

func (s *Service) processOne(ctx context.Context, workerID string, d Deployment) error {
	req := ProvisionRequest{
		DeploymentID:           d.ID,
		EnvironmentID:          d.EnvironmentID,
		ConfigurationVersionID: d.ConfigurationVersionID,
		ProviderRequestID:      d.ProviderRequestID,
		TargetMode:             d.TargetMode,
	}
	res, err := s.provider.Provision(ctx, req)
	if err != nil && !errors.Is(err, ErrProviderUnavailable) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return s.repo.MarkFailed(ctx, d, workerID, "provider_ambiguous", "provider call timed out; reconciliation required", true)
		}
		return s.repo.MarkFailed(ctx, d, workerID, "provider_error", "provider call failed", false)
	}
	switch res.Outcome {
	case OutcomeSuccess:
		if err := s.repo.MarkReady(ctx, d, workerID, res); err != nil {
			if errors.Is(err, ErrStaleWorker) {
				return err
			}
			return s.repo.MarkFailed(ctx, d, workerID, "invalid_provider_evidence", "provider evidence rejected", true)
		}
		return nil
	case OutcomeUnavailable:
		return s.repo.MarkFailed(ctx, d, workerID, res.Code, res.Message, true)
	case OutcomeFailure:
		return s.repo.MarkFailed(ctx, d, workerID, res.Code, res.Message, true)
	case OutcomeRetryable:
		return s.repo.MarkFailed(ctx, d, workerID, res.Code, res.Message, false)
	case OutcomeAmbiguous:
		return s.repo.MarkFailed(ctx, d, workerID, "provider_ambiguous", "provider result ambiguous; reconciliation required", true)
	default:
		return s.repo.MarkFailed(ctx, d, workerID, "provider_unknown", "unknown provider outcome", true)
	}
}
