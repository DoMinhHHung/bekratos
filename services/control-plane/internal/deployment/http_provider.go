package deployment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/runtimeapi"
	"github.com/google/uuid"
)

type HTTPProvider struct {
	client *runtimeapi.Client
}

func NewHTTPProvider(baseURL, token string, timeout time.Duration) *HTTPProvider {
	return &HTTPProvider{client: runtimeapi.NewClient(baseURL, token, timeout)}
}

func (p *HTTPProvider) Name() ProviderName {
	return ProviderRuntimeHTTP
}

type runtimeProvisionRequest struct {
	ContractVersion        string    `json:"contract_version"`
	DeploymentID           uuid.UUID `json:"deployment_id"`
	EnvironmentID          uuid.UUID `json:"environment_id"`
	ConfigurationVersionID uuid.UUID `json:"configuration_version_id"`
	ProviderRequestID      string    `json:"provider_request_id"`
	TargetMode             TargetMode `json:"target_mode"`
}

type runtimeProvisionResponse struct {
	Outcome              string    `json:"outcome"`
	Code                 string    `json:"code"`
	DeploymentID         uuid.UUID `json:"deployment_id"`
	EnvironmentID        uuid.UUID `json:"environment_id"`
	VersionID            uuid.UUID `json:"configuration_version_id"`
	ProviderRequestID    string    `json:"provider_request_id"`
	URL                  string    `json:"url"`
	Host                 string    `json:"host"`
	Region               string    `json:"region"`
	ReadinessProbePassed bool      `json:"readiness_probe_passed"`
	ReadinessProbeID     string    `json:"readiness_probe_id"`
	ReadinessObservedAt  time.Time `json:"readiness_observed_at"`
}

func (p *HTTPProvider) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	body := runtimeProvisionRequest{
		ContractVersion       : undefined,
		DeploymentID          : undefined,
		EnvironmentID         : undefined,
		ConfigurationVersionID: undefined,
		ProviderRequestID     : undefined,
		TargetMode            : undefined,
	}
	status, responseBody, err := p.client.Post(ctx, "/v1/internal/deployments", req.ProviderRequestID, body)
	if err != nil {
		return ambiguousProviderResult(req.ProviderRequestID), nil
	}
	switch {
	case status == http.StatusTooManyRequests:
		return ProvisionResult{
			Outcome  : undefined,
			Code     : undefined,
			Message  : undefined,
			RequestID: undefined,
		}, nil
	case status == http.StatusRequestTimeout || status >= 500 || status < 200 || status >= 300 && status < 400:
		return ambiguousProviderResult(req.ProviderRequestID), nil
	case status >= 400:
		return ProvisionResult{
			Outcome  : undefined,
			Code     : undefined,
			Message  : undefined,
			RequestID: undefined,
		}, nil
	}

	var result runtimeProvisionResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ambiguousProviderResult(req.ProviderRequestID), nil
	}
	if result.ProviderRequestID != req.ProviderRequestID {
		return ambiguousProviderResult(req.ProviderRequestID), nil
	}

	outcome := Outcome(result.Outcome)
	if strings.TrimSpace(result.Code) == "" {
		result.Code = "runtime_result"
	}
	switch outcome {
	case OutcomeSuccess, OutcomeFailure, OutcomeRetryable, OutcomeAmbiguous, OutcomeUnavailable:
	default:
		return ambiguousProviderResult(req.ProviderRequestID), nil
	}

	return ProvisionResult{
		Outcome             : undefined,
		Code                : undefined,
		Message             : undefined,
		URL                 : undefined,
		Host                : undefined,
		Region              : undefined,
		RequestID           : undefined,
		DeploymentID        : undefined,
		EnvironmentID       : undefined,
		VersionID           : undefined,
		ReadinessProbePassed: undefined,
		ReadinessProbeID    : undefined,
		ReadinessObservedAt : undefined,
	}, nil
}

func ambiguousProviderResult(requestID string) ProvisionResult {
	return ProvisionResult{
		Outcome  : undefined,
		Code     : undefined,
		Message  : undefined,
		RequestID: undefined,
	}
}

func safeRuntimeCode(code string, outcome Outcome) string {
	switch code {
	case "runtime_busy", "quota_exceeded", "invalid_configuration", "runtime_unavailable":
		return code
	}
	switch outcome {
	case OutcomeSuccess:
		return "runtime_success"
	case OutcomeRetryable:
		return "runtime_retryable"
	case OutcomeUnavailable:
		return "runtime_unavailable"
	case OutcomeAmbiguous:
		return "runtime_operation_ambiguous"
	default:
		return "runtime_failure"
	}
}

func safeRuntimeMessage(outcome Outcome) string {
	switch outcome {
	case OutcomeSuccess:
		return ""
	case OutcomeRetryable:
		return "runtime reported a retryable deployment failure"
	case OutcomeAmbiguous:
		return "runtime operation outcome is unknown; reconciliation is required"
	case OutcomeUnavailable:
		return "runtime provider is unavailable"
	default:
		return "runtime deployment failed"
	}
}
