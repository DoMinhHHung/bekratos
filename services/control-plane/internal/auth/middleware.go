package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type Middleware struct {
	validator *Validator
	identity  *IdentityClient
	log       *slog.Logger
}

func NewMiddleware(validator *Validator, identity *IdentityClient, log *slog.Logger) *Middleware {
	if log == nil {
		log = slog.Default()
	}
	return &Middleware{validator: validator, identity: identity, log: log}
}

func (m *Middleware) RequirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "" {
			m.log.Warn("rejected client-supplied identity header", "path", r.URL.Path)
		}

		rawToken, err := extractBearer(r.Header.Get("Authorization"))
		if err != nil {
			writeUnauthorized(w)
			return
		}
		principal, err := m.validator.ValidateToken(r.Context(), rawToken)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		if m.identity == nil {
			writeIdentityUnavailable(w)
			return
		}

		identity, err := m.identity.Introspect(r.Context(), rawToken)
		if err != nil {
			if errors.Is(err, ErrIdentityUnauthenticated) {
				writeUnauthorized(w)
			} else {
				writeIdentityUnavailable(w)
			}
			return
		}
		if !identity.Active ||
			identity.UserID != principal.UserID.String() ||
			identity.SessionID != principal.SessionID.String() ||
			identity.Issuer != principal.Issuer ||
			identity.Audience != principal.Audience ||
			identity.ExpiresAt != principal.ExpiresAt ||
			!identity.EmailVerified ||
			identity.VerifiedPrimaryEmail == nil ||
			strings.TrimSpace(*identity.VerifiedPrimaryEmail) == "" {
			writeUnauthorized(w)
			return
		}

		principal.PrimaryEmail = *identity.VerifiedPrimaryEmail
		principal.EmailVerified = true
		ctx := ContextWithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"invalid or missing credentials"}`))
}

func writeIdentityUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"identity_unavailable","message":"identity session verification is temporarily unavailable"}`))
}
