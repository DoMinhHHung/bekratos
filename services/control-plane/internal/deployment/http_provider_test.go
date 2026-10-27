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
			"outcome": "success",
			"deployment_id": deploymentID,
			"environment_id": environmentID,
			"configuration_version_id": versionID,
			"provider_request_id": requestID,
			"url": "https://app.runtime.example/deployments/" + deploymentID.String(),
			"host": "app.runtime.example",
			"region": "ap-southeast-1",
			"readiness_probe_passed": true,
			"readiness_probe_id": "probe-" + deploymentID.String(),
			"readiness_observed_at": observedAt,
		})
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := provider.Provision(t.Context(), ProvisionRequest{
		DeploymentID: deploymentID,
		EnvironmentID: environmentID,
		ConfigurationVersionID: versionID,
		ProviderRequestID: requestID,
		TargetMode: TargetModeShared,
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
		DeploymentID: uuid.New(),
		EnvironmentID: uuid.New(),
		ConfigurationVersionID: uuid.New(),
		ProviderRequestID: "dep-test-ambiguous",
		TargetMode: TargetModeShared,
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
