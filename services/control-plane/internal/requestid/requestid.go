package requestid

import (
	"context"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

type contextKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), contextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKey{}).(string); ok {
		return v
	}
	return ""
}
