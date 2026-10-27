package runtimeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostSendsServiceCredentialsAndStableIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/operation" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-runtime-token-0123456789abcdef" {
			t.Fatal("missing Runtime service credential")
		}
		if r.Header.Get("Idempotency-Key") != "operation-123" {
			t.Fatal("missing stable idempotency key")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	status, body, err := client.Post(context.Background(), "/operation", "operation-123", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("Post returned error: %v", err)
	}
	if status != http.StatusAccepted || string(body) != `{"accepted":true}` {
		t.Fatalf("unexpected response: status=%d body=%s", status, body)
	}
}

func TestPostDoesNotFollowRedirects(t *testing.T) {
	var targetCalled bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/redirected", http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := NewClient(source.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	status, _, err := client.Post(context.Background(), "/operation", "operation-456", map[string]string{"ok": "yes"})
	if err != nil {
		t.Fatalf("Post returned error: %v", err)
	}
	if status != http.StatusTemporaryRedirect || targetCalled {
		t.Fatalf("redirect was followed or status changed: status=%d targetCalled=%v", status, targetCalled)
	}
}

func TestPostRejectsUnconfiguredClientAndOversizedPayload(t *testing.T) {
	if _, _, err := NewClient("", "", time.Second).Post(context.Background(), "/operation", "key", map[string]string{"x": "y"}); err == nil {
		t.Fatal("expected unconfigured client to fail closed")
	}
	client := NewClient("http://127.0.0.1:1", strings.Repeat("x", 32), time.Second)
	large := strings.Repeat("x", maxRequestBytes+1)
	if _, _, err := client.Post(context.Background(), "/operation", "key", map[string]string{"payload": large}); err == nil {
		t.Fatal("expected oversized payload to fail before making a request")
	}
}
