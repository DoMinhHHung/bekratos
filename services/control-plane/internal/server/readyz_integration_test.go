package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
)

func TestReadyzWithLiveDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	h := NewHandlers(pool, slog.Default(), 2*time.Second)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.Readyz(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ready" {
		t.Fatalf("body = %v", body)
	}
}
