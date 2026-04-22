package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recoverer catches panics from downstream handlers and returns a 500.
func Recoverer() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("panic in handler: %v\n%s", rec, debug.Stack())
					http.Error(w, `{"error":{"code":"internal_error","message":"internal server error"}}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
