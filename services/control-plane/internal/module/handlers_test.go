package module

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchBudgetErrorResponse(t *testing.T) {
	response := httptest.NewRecorder()
	mapErr(response, ErrSearchBudget)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusConflict)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "resolution_budget_exceeded" || body["message"] != "dependency resolution limit exceeded" {
		t.Fatalf("unstable budget response: %+v", body)
	}
}

func TestPublishedAtJSONDoesNotExposeZeroTimestamp(t *testing.T) {
	if got := publishedAtJSON(nil); got != nil {
		t.Fatalf("nil publication time serialized as %q", *got)
	}
	zero := time.Time{}
	if got := publishedAtJSON(&zero); got != nil {
		t.Fatalf("zero publication time serialized as %q", *got)
	}
	published := time.Date(2026, 10, 27, 1, 0, 0, 123, time.FixedZone("test", 3600))
	got := publishedAtJSON(&published)
	if got == nil || *got != "2026-10-27T00:00:00.000000123Z" {
		t.Fatalf("published timestamp serialization=%v", got)
	}
}
