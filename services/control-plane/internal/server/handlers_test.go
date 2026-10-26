package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestHealthzOK(t *testing.T) {
	h := NewHandlers(nil, slog.Default(), 2*time.Second)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.Healthz(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	h := NewHandlers(nil, slog.Default(), 2*time.Second)
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.Healthz(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestReadyzUnavailableWithoutPool(t *testing.T) {
	h := NewHandlers(nil, slog.Default(), 100*time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.Readyz(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "not_ready" {
		t.Errorf("body = %v", body)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if id == "" {
			t.Error("request id missing in context")
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header missing")
	}
}

func TestRequestIDPreservesClientValue(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if id != "client-id-123" {
			t.Errorf("id = %q", id)
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-id-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Header().Get("X-Request-ID") != "client-id-123" {
		t.Errorf("header = %q", rr.Header().Get("X-Request-ID"))
	}
}

func TestMaxBytes(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 10))
		w.WriteHeader(http.StatusOK)
	})
	handler := MaxBytes(5)(next)
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
