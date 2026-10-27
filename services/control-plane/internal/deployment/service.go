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
	if batch <= 0 {
		batch = 1
	}
	if lease <= 0 {
		return 0, ErrInvalidInput
	}
	processed := 0
	for processed < batch {
		claimed, err := s.repo.Claim(ctx, workerID, lease, 1)
		if err != nil {
			return processed, err
		}
		if len(claimed) == 0 {
			break
		}
		d := claimed[0]
		providerTimeout := lease - 3*time.Second
		if providerTimeout <= 0 {
			providerTimeout = lease / 2
		}
		if providerTimeout <= 0 {
			providerTimeout = time.Millisecond
		}
		providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
		if err := s.processOne(providerCtx, workerID, d); err != nil {
			s.log.Warn("deployment process failed", "deployment_id", d.ID.String(), "error_category", "outcome_persistence_or_validation_failed")
		}
		cancel()
		processed++
	}
	return processed, nil
}

func (s *Service) processOne(ctx context.Context, workerID string, d Deployment) error {
	req := ProvisionRequest{
		DeploymentID:           d.ID,
		EnvironmentID:          d.EnvironmentID,
		ConfigurationVersionID: d.ConfigurationVersionID,
		ProviderRequestID:      d.ProviderRequestID,
		TargetMode:             d.TargetMode,
	}
	res, providerErr := s.provider.Provision(ctx, req)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()

	if ctx.Err() != nil || errors.Is(providerErr, context.DeadlineExceeded) || errors.Is(providerErr, context.Canceled) {
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_ambiguous", "provider result ambiguous; reconciliation required", true)
	}
	if errors.Is(providerErr, ErrProviderUnavailable) {
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_unavailable", "deployment provider is unavailable", true)
	}
	if providerErr != nil {
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_error", "deployment provider request failed", false)
	}

	switch res.Outcome {
	case OutcomeSuccess:
		if err := s.repo.MarkReady(persistCtx, d, workerID, res); err != nil {
			if errors.Is(err, ErrStaleWorker) {
				return err
			}
			return s.repo.MarkFailed(persistCtx, d, workerID, "invalid_provider_evidence", "deployment provider returned invalid readiness evidence", true)
		}
		return nil
	case OutcomeUnavailable:
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_unavailable", "deployment provider is unavailable", true)
	case OutcomeFailure:
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_failure", "deployment provider failed", true)
	case OutcomeRetryable:
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_retryable", "deployment provider reported a retryable failure", false)
	case OutcomeAmbiguous:
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_ambiguous", "provider result ambiguous; reconciliation required", true)
	default:
		return s.repo.MarkFailed(persistCtx, d, workerID, "provider_unknown", "deployment provider returned an unsupported outcome", true)
	}
}
