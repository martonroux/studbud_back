package middleware

import (
	"net/http"
	"strings"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/myErrors"
)

// Auth parses the Bearer token and attaches identity to the request context.
// Requests without a token are rejected with 401.
func Auth(s *jwtsigner.Signer, isAdmin func(uid int64) (bool, error)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			claims, err := s.Verify(strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			admin := false
			if isAdmin != nil {
				if ok, err := isAdmin(claims.UID); err == nil {
					admin = ok
				}
			}
			ctx := authctx.WithIdentity(r.Context(), claims.UID, claims.EmailVerified, admin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
