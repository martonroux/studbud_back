package middleware

import (
	"io"
	"log"
	"net/http"
	"runtime/debug"
)

// panicBody is the JSON error envelope returned after a recovered panic.
const panicBody = `{"error":{"code":"internal_error","message":"internal server error"}}`

// Recoverer catches panics from downstream handlers and returns a 500 JSON envelope.
func Recoverer() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("panic in handler: %v\n%s", rec, debug.Stack())
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, panicBody)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
