package jwt

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the application's JWT claim set.
type Claims struct {
	UID           int64 `json:"uid"`            // UID is the authenticated user ID
	EmailVerified bool  `json:"email_verified"` // EmailVerified reflects the user's verification state at token issue time
	jwt.RegisteredClaims
}

// Signer signs and verifies tokens using a shared secret.
type Signer struct {
	secret []byte        // secret signs HS256 tokens
	issuer string        // issuer is embedded as the "iss" claim
	ttl    time.Duration // ttl defines how long tokens remain valid after issuance
}

// NewSigner constructs a Signer with the given secret, issuer, and TTL.
func NewSigner(secret, issuer string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

// Sign returns an HS256-signed token containing uid and email_verified claims.
func (s *Signer) Sign(c Claims) (string, error) {
	now := time.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    s.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	str, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign token:\n%w", err)
	}
	return str, nil
}

// Verify parses and validates a token, returning the decoded claims.
// Returns an error for bad signatures, expired tokens, or wrong algorithms.
func (s *Signer) Verify(raw string) (*Claims, error) {
	out := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, out, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("unexpected algorithm: %s", t.Method.Alg())
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token:\n%w", err)
	}
	if !tok.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return out, nil
}
