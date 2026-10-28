package auth

import (
	"errors"
	"log/slog"
	"net/http"
)

type Middleware struct {
	validator *Validator
	identity  *IdentityProfileVerifier
	log       *slog.Logger
}

func NewMiddleware(validator *Validator, log *slog.Logger) *Middleware {
	return &Middleware{validator: validator, log: log}
}

func NewMiddlewareWithIdentity(validator *Validator, log *slog.Logger, identity *IdentityProfileVerifier) *Middleware {
	return &Middleware{validator: validator, identity: identity, log: log}
}

func (m *Middleware) RequirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "" {
			m.log.Warn("rejected client-supplied identity header",
				"path", r.URL.Path,
			)
		}
		authorization := r.Header.Get("Authorization")
		principal, err := m.validator.ValidateBearer(r.Context(), authorization)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		if m.identity != nil {
			verified, identityErr := m.identity.Verify(r.Context(), authorization, principal.UserID, principal.SessionID)
			if errors.Is(identityErr, ErrIdentityUnauthorized) {
				writeUnauthorized(w)
				return
			}
			if identityErr != nil {
				writeUnavailable(w)
				return
			}
			principal.EmailVerified = verified
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

func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"service_unavailable","message":"identity verification unavailable"}`))
}
