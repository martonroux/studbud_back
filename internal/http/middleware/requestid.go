package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ridKey int

const requestIDKey ridKey = 0

// RequestID ensures every request carries an X-Request-Id. If the client
// provides one, it is echoed back; otherwise a UUID is generated.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext returns the id stored on ctx by RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}
