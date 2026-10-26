package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handlers struct {
	pool             *pgxpool.Pool
	log              *slog.Logger
	readinessTimeout time.Duration
}

func NewHandlers(pool *pgxpool.Pool, log *slog.Logger, readinessTimeout time.Duration) *Handlers {
	return &Handlers{
		pool:             pool,
		log:              log,
		readinessTimeout: readinessTimeout,
	}
}

func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if err := db.Ping(r.Context(), h.pool, h.readinessTimeout); err != nil {
		h.log.Warn("readiness check failed",
			"request_id", RequestIDFromContext(r.Context()),
			"error", err.Error(),
		)
		writeJSONError(w, http.StatusServiceUnavailable, "not_ready", "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}
