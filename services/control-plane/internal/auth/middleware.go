package auth

import (
	"log/slog"
	"net/http"
)

type Middleware struct {
	validator *Validator
	log       *slog.Logger
}

func NewMiddleware(validator *Validator, log *slog.Logger) *Middleware {
	return &Middleware{validator: validator, log: log}
}

func (m *Middleware) RequirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "" {
			m.log.Warn("rejected client-supplied identity header",
				"path", r.URL.Path,
			)
		}
		principal, err := m.validator.ValidateBearer(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeUnauthorized(w)
			return
		}
		ctx := ContextWithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid or missing credentials"}`))
}
