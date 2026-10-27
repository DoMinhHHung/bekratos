package deployment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPProviderProvisionPreservesIdempotencyAndValidatesResponseEnvelope(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "dep-" + deploymentID.String()
	observedAt := time.Now().UTC().Truncate(time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/internal/deployments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-runtime-token-0123456789abcdef" {
			t.Fatal("runtime authorization header missing or incorrect")
		}
		if r.Header.Get("Idempotency-Key") != requestID {
			t.Fatal("stable provider request ID was not used as Idempotency-Key")
		}
		var request runtimeProvisionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.ContractVersion != "v1" ||
			request.DeploymentID != deploymentID ||
			request.EnvironmentID != environmentID ||
			request.ConfigurationVersionID != versionID ||
			request.ProviderRequestID != requestID {
			t.Fatalf("request identity mismatch: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"outcome"                 : undefined,
			"deployment_id"           : undefined,
			"environment_id"          : undefined,
			"configuration_version_id": undefined,
			"provider_request_id"     : undefined,
			"url"                     : undefined,
			"host"                    : undefined,
			"region"                  : undefined,
			"readiness_probe_passed"  : undefined,
			"readiness_probe_id"      : undefined,
			"readiness_observed_at"   : undefined,
		})
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{
		DeploymentID          : undefined,
		EnvironmentID         : undefined,
		ConfigurationVersionID: undefined,
		ProviderRequestID     : undefined,
		TargetMode            : undefined,
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeSuccess ||
		result.RequestID != requestID ||
		result.DeploymentID != deploymentID ||
		result.EnvironmentID != environmentID ||
		result.VersionID != versionID ||
		!result.ReadinessProbePassed ||
		result.ReadinessProbeID == "" ||
		!result.ReadinessObservedAt.Equal(observedAt) {
		t.Fatalf("unexpected provision result: %+v", result)
	}
}

func TestHTTPProviderTreatsServerErrorsAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal diagnostic that must not escape", http.StatusBadGateway)
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{
		DeploymentID          : undefined,
		EnvironmentID         : undefined,
		ConfigurationVersionID: undefined,
		ProviderRequestID     : undefined,
		TargetMode            : undefined,
	})
	if err != nil {
		t.Fatalf("Provision returned unexpected transport error: %v", err)
	}
	if result.Outcome != OutcomeAmbiguous || result.Code != "runtime_operation_ambiguous" {
		t.Fatalf("expected ambiguous result, got %+v", result)
	}
	if result.Message == "" || result.Message == "internal diagnostic that must not escape" {
		t.Fatalf("provider diagnostic leaked: %+v", result)
	}
}

func TestHTTPProviderTreatsPreAcceptanceRateLimitAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{
		ProviderRequestID: "dep-rate-limited",
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeRetryable {
		t.Fatalf("expected retryable result, got %+v", result)
	}
}

func TestHTTPProviderRejectsSuccessWithMismatchedIdentityAsAmbiguous(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "dep-" + deploymentID.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"outcome"                 : undefined,
			"deployment_id"           : undefined,
			"environment_id"          : undefined,
			"configuration_version_id": undefined,
			"provider_request_id"     : undefined,
			"readiness_probe_passed"  : undefined,
			"readiness_probe_id"      : undefined,
			"readiness_observed_at"   : undefined,
			"url"                     : undefined,
		})
	}))
	defer server.Close()
	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{
		DeploymentID          : undefined,
		EnvironmentID         : undefined,
		ConfigurationVersionID: undefined,
		ProviderRequestID     : undefined,
		TargetMode            : undefined,
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeAmbiguous {
		t.Fatalf("expected mismatched response to be ambiguous, got %+v", result)
	}
}

func TestHTTPProviderRejectsUnknownOutcomeAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"outcome"            : undefined,
			"provider_request_id": undefined,
		})
	}))
	defer server.Close()
	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{ProviderRequestID: "dep-unknown"})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeAmbiguous {
		t.Fatalf("expected unknown outcome to be ambiguous, got %+v", result)
	}
}
