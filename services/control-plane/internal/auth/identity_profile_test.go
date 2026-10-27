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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer platform-token" {
			t.Error("authorization header was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": userID.String(), "status": "active", "email_verified": true,
		})
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, time.Second)
	verified, err := verifier.Verify(context.Background(), "Bearer platform-token", userID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !verified {
		t.Fatal("expected verified primary email")
	}
}

func TestIdentityProfileVerifierRejectsRevokedSession(t *testing.T) {
	userID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer expired-token", userID)
	if !errors.Is(err, ErrIdentityUnauthorized) {
		t.Fatalf("error = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityProfileVerifierFailsClosedOnUpstreamFailure(t *testing.T) {
	userID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID)
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("error = %v, want ErrIdentityUnavailable", err)
	}
}

func TestIdentityProfileVerifierRejectsOversizedResponse(t *testing.T) {
	userID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + userID.String() + `","status":"active","email_verified":true,"padding":"` + string(make([]byte, maxIdentityProfileBytes)) + `"}`))
	}))
	defer server.Close()

	verifier := NewIdentityProfileVerifier(server.URL, time.Second)
	_, err := verifier.Verify(context.Background(), "Bearer platform-token", userID)
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("error = %v, want ErrIdentityUnavailable", err)
	}
}
