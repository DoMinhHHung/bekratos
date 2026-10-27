package configversion

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/runtimeapi"
	"github.com/google/uuid"
)

type HTTPApplyAdapter struct {
	client *runtimeapi.Client
}

func NewHTTPApplyAdapter(baseURL, token string, timeout time.Duration) *HTTPApplyAdapter {
	return &HTTPApplyAdapter{client: runtimeapi.NewClient(baseURL, token, timeout)}
}

type runtimeApplyRequest struct {
	ContractVersion         string          `json:"contract_version"`
	EnvironmentID           uuid.UUID       `json:"environment_id"`
	ConfigurationVersionID  uuid.UUID      `json:"configuration_version_id"`
	ManifestFormatVersion   int             `json:"manifest_format_version"`
	ManifestSHA256          string          `json:"manifest_sha256"`
	Manifest                json.RawMessage `json:"manifest"`
	ApplyRequestID          string          `json:"apply_request_id"`
	ExpectedActiveVersionID *uuid.UUID    `json:"expected_active_version_id"`
}

type runtimeApplyResponse struct {
	Accepted                bool       `json:"accepted"`
	EnvironmentID           uuid.UUID  `json:"environment_id"`
	AppliedVersion          uuid.UUID  `json:"applied_configuration_version_id"`
	AppliedSHA256           string     `json:"applied_sha256"`
	ExpectedActiveVersionID *uuid.UUID `json:"expected_active_version_id"`
	Code                    string     `json:"code"`
	NeedsReconciliation     bool       `json:"needs_reconciliation"`
}

type AmbiguousApplyError struct{}

func (AmbiguousApplyError) Error() string {
	return "runtime apply outcome is ambiguous"
}

func (a *HTTPApplyAdapter) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	if !json.Valid([]byte(req.ManifestCanonical)) {
		return ApplyResult{}, AmbiguousApplyError{}
	}
	body := runtimeApplyRequest{
		ContractVersion        : undefined,
		EnvironmentID          : undefined,
		ConfigurationVersionID : undefined,
		ManifestFormatVersion  : undefined,
		ManifestSHA256         : undefined,
		Manifest               : undefined,
		ApplyRequestID         : undefined,
		ExpectedActiveVersionID: undefined,
	}
	status, responseBody, err := a.client.Post(ctx, "/v1/internal/configuration/apply", req.ApplyRequestID, body)
	if err != nil || status == http.StatusRequestTimeout || status >= 500 ||
		status < 200 || status >= 300 && status < 400 {
		return ApplyResult{}, AmbiguousApplyError{}
	}
	if status == http.StatusTooManyRequests {
		return ApplyResult{}, RetryableApplyError{
			Code   : undefined,
			Message: undefined,
		}
	}
	if status < 200 || status >= 300 {
		return ApplyResult{
			Accepted: undefined,
			Code    : undefined,
			Message : undefined,
		}, nil
	}

	var decoded runtimeApplyResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return ApplyResult{}, AmbiguousApplyError{}
	}
	return ApplyResult{
		Accepted               : undefined,
		AppliedVersion         : undefined,
		AppliedSHA256          : undefined,
		EnvironmentID          : undefined,
		ExpectedActiveVersionID: undefined,
		Code                   : undefined,
		Message                : undefined,
		NeedsReconciliation    : undefined,
	}, nil
}
