package deployment

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ProvisionRequest struct {
	DeploymentID           uuid.UUID
	EnvironmentID          uuid.UUID
	ConfigurationVersionID uuid.UUID
	ProviderRequestID      string
	TargetMode             TargetMode
}

type ProvisionResult struct {
	Outcome                Outcome
	Code                   string
	Message                string
	URL                    string
	Host                   string
	Region                 string
	RequestID              string
	EnvironmentID          uuid.UUID
	VersionID              uuid.UUID
	ReadinessProbePassed   bool
	ReadinessProbeID       string
	ReadinessObservedAt    time.Time
}

type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeFailure     Outcome = "failure"
	OutcomeRetryable   Outcome = "retryable"
	OutcomeAmbiguous   Outcome = "ambiguous"
	OutcomeUnavailable Outcome = "unavailable"
)

type Provider interface {
	Name() ProviderName
	Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error)
}

type UnavailableProvider struct{}

func NewUnavailableProvider() *UnavailableProvider {
	return &UnavailableProvider{}
}

func (p *UnavailableProvider) Name() ProviderName { return ProviderUnavailable }

func (p *UnavailableProvider) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	return ProvisionResult{
		Outcome:   OutcomeUnavailable,
		Code:      "provider_unavailable",
		Message:   "no production deployment provider is configured",
		RequestID: req.ProviderRequestID,
	}, ErrProviderUnavailable
}

type RecordingProvider struct {
	Result ProvisionResult
	Err    error
	Calls  []ProvisionRequest
}

func (p *RecordingProvider) Name() ProviderName { return ProviderName("test") }

func (p *RecordingProvider) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	p.Calls = append(p.Calls, req)
	if p.Err != nil {
		return ProvisionResult{}, p.Err
	}
	res := p.Result
	if res.RequestID == "" {
		res.RequestID = req.ProviderRequestID
	}
	return res, nil
}
