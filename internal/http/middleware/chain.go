package middleware

import "net/http"

// Middleware wraps an http.Handler to add cross-cutting behavior.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple Middleware into one. Middlewares run outer→inner
// in the order given: Chain(a, b)(h) == a(b(h)).
func Chain(ms ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(ms) - 1; i >= 0; i-- {
			next = ms[i](next)
		}
		return next
	}
}
