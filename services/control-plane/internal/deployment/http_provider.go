package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxRuntimeResponseBytes = 32 * 1024

type HTTPProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

type runtimeProvisionRequest struct {
	DeploymentID           uuid.UUID  `json:"deployment_id"`
	EnvironmentID          uuid.UUID  `json:"environment_id"`
	ConfigurationVersionID uuid.UUID  `json:"configuration_version_id"`
	IdempotencyKey         string     `json:"provider_request_id"`
	TargetMode             TargetMode `json:"target_mode"`
}

type runtimeProvisionResponse struct {
	Outcome              Outcome   `json:"outcome"`
	Code                 string    `json:"code"`
	Message              string    `json:"message"`
	URL                  string    `json:"url"`
	Host                 string    `json:"host"`
	Region               string    `json:"region"`
	RequestID            string    `json:"provider_request_id"`
	DeploymentID         uuid.UUID `json:"deployment_id"`
	EnvironmentID        uuid.UUID `json:"environment_id"`
	VersionID            uuid.UUID `json:"configuration_version_id"`
	ReadinessProbePassed bool      `json:"readiness_probe_passed"`
	ReadinessProbeID     string    `json:"readiness_probe_id"`
	ReadinessObservedAt  time.Time `json:"readiness_observed_at"`
}

func NewHTTPProvider(baseURL, token string, timeout time.Duration) *HTTPProvider {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *HTTPProvider) Name() ProviderName {
	return ProviderName("runtime_http")
}

func (p *HTTPProvider) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	if p == nil || p.baseURL == "" || p.token == "" {
		return ProvisionResult{Outcome: OutcomeUnavailable, Code: "provider_unavailable", RequestID: req.ProviderRequestID}, ErrProviderUnavailable
	}

	payload := runtimeProvisionRequest{
		DeploymentID:           req.DeploymentID,
		EnvironmentID:          req.EnvironmentID,
		ConfigurationVersionID: req.ConfigurationVersionID,
		IdempotencyKey:         req.ProviderRequestID,
		TargetMode:             req.TargetMode,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProvisionResult{}, errors.New("runtime request serialization failed")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/deployments", bytes.NewReader(body))
	if err != nil {
		return ProvisionResult{}, errors.New("runtime request construction failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.ProviderRequestID)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return ProvisionResult{Outcome: OutcomeAmbiguous, Code: "provider_ambiguous", RequestID: req.ProviderRequestID}, fmt.Errorf("runtime provider outcome ambiguous")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return ProvisionResult{Outcome: OutcomeRetryable, Code: "provider_rate_limited", RequestID: req.ProviderRequestID}, nil
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
		return ProvisionResult{Outcome: OutcomeAmbiguous, Code: "provider_ambiguous", RequestID: req.ProviderRequestID}, fmt.Errorf("runtime provider outcome ambiguous")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProvisionResult{Outcome: OutcomeFailure, Code: "provider_rejected", RequestID: req.ProviderRequestID}, nil
	}

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeResponseBytes+1))
	if err != nil || len(responseBody) > maxRuntimeResponseBytes {
		return ProvisionResult{Outcome: OutcomeAmbiguous, Code: "provider_ambiguous", RequestID: req.ProviderRequestID}, fmt.Errorf("runtime provider response could not be validated")
	}
	var result runtimeProvisionResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ProvisionResult{Outcome: OutcomeAmbiguous, Code: "provider_ambiguous", RequestID: req.ProviderRequestID}, fmt.Errorf("runtime provider response could not be validated")
	}
	if result.Outcome == OutcomeSuccess &&
		(result.DeploymentID != req.DeploymentID ||
			result.EnvironmentID != req.EnvironmentID ||
			result.VersionID != req.ConfigurationVersionID ||
			result.RequestID != req.ProviderRequestID ||
			!result.ReadinessProbePassed ||
			result.ReadinessProbeID == "" ||
			result.ReadinessObservedAt.IsZero() ||
			result.ReadinessObservedAt.After(time.Now().Add(30*time.Second)) ||
			time.Since(result.ReadinessObservedAt) > 2*time.Minute ||
			!validRuntimeLocation(result.URL, result.Host) ||
			len(result.Region) > 128 ||
			len(result.Code) > 128 ||
			len(result.Message) > 1024) {
		return ProvisionResult{
			Outcome:   OutcomeAmbiguous,
			Code:      "invalid_readiness_evidence",
			RequestID: req.ProviderRequestID,
		}, nil
	}

	return ProvisionResult{
		Outcome:              result.Outcome,
		Code:                 result.Code,
		Message:              result.Message,
		URL:                  result.URL,
		Host:                 result.Host,
		Region:               result.Region,
		RequestID:            result.RequestID,
		DeploymentID:         result.DeploymentID,
		EnvironmentID:        result.EnvironmentID,
		VersionID:            result.VersionID,
		ReadinessProbePassed: result.ReadinessProbePassed,
		ReadinessProbeID:     result.ReadinessProbeID,
		ReadinessObservedAt:  result.ReadinessObservedAt,
	}, nil
}


func validRuntimeLocation(rawURL, host string) bool {
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || !u.IsAbs() || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(rawURL, " \t\r\n") {
			return false
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			return false
		}
		if host != "" && !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(host, ".")) {
			return false
		}
	}
	if host == "" {
		return true
	}
	if strings.ContainsAny(host, " /\\?#@\t\r\n") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "localhost" {
		return true
	}
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
