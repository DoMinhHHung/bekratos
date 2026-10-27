package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIdentityProfileVerifierReturnsTrustedEmailState(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer platform-token" {
			t.Error("authorization header was not forwarded")
		}
		if r.Header.Get("X-Control-Plane-Service-Token") != "local-test-identity-service-token-32-characters" {
			t.Error("missing Identity service authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subject": userID.String(),
			"session_id": sessionID.String(),
			"account_status": "active",
			"primary_email_verified": true,
		})
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", time.Second)
	verified, err := verifier.Verify(context.Background(), "Bearer platform-token", userID, sessionID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !verified {
		t.Fatal("expected verified primary email")
	}
}

func TestIdentityProfileVerifierRejectsRevokedSession(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer expired-token", userID, sessionID)
	if !errors.Is(err, ErrIdentityUnauthorized) {
		t.Fatalf("error = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityProfileVerifierFailsClosedOnUpstreamFailure(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID, sessionID)
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("error = %v, want ErrIdentityUnavailable", err)
	}
}

func TestIdentityProfileVerifierRejectsMismatchedSession(t *testing.T) {
	userID := uuid.New()
	expectedSessionID := uuid.New()
	serverSessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subject": userID.String(),
			"session_id": serverSessionID.String(),
			"account_status": "active",
			"primary_email_verified": true,
		})
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID, expectedSessionID)
	if !errors.Is(err, ErrIdentityUnauthorized) {
		t.Fatalf("error = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityProfileVerifierTimesOutAndFailsClosed(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", 10*time.Millisecond)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID, sessionID)
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("error = %v, want ErrIdentityUnavailable", err)
	}
}

func TestIdentityProfileVerifierRejectsOversizedResponse(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"subject":"` + userID.String() + `","session_id":"` + sessionID.String() + `","account_status":"active","primary_email_verified":true,"padding":"` + string(make([]byte, maxIdentityProfileBytes)) + `"}`))
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, "local-test-identity-service-token-32-characters", time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID, sessionID)
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("error = %v, want ErrIdentityUnavailable", err)
	}
}
