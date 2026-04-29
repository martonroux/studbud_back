package middleware

import "net/http"

// CORS attaches permissive CORS headers. The first entry in allowedOrigins is
// the default reflected when the request has no matching Origin header; any
// request whose Origin matches an entry in the list gets that exact origin
// echoed back. Preflight OPTIONS requests short-circuit with 204.
func CORS(allowedOrigins ...string) Middleware {
	fallback := ""
	if len(allowedOrigins) > 0 {
		fallback = allowedOrigins[0]
	}
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o != "" {
			allowed[o] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; ok {
				h.Set("Access-Control-Allow-Origin", origin)
			} else {
				h.Set("Access-Control-Allow-Origin", fallback)
			}
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
			h.Set("Access-Control-Expose-Headers", "X-Request-Id")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
