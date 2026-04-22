package emailverification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/email"
	"studbud/backend/internal/myErrors"
)

// Service owns verification token issuance, verification, and throttling.
type Service struct {
	db          *pgxpool.Pool // db is the shared PostgreSQL connection pool
	mailer      email.Sender  // mailer sends outbound verification emails
	frontendURL string        // frontendURL is the base URL used to build the verification link
	ttl         time.Duration // ttl is how long a verification token remains valid
}

// NewService constructs the email verification service.
func NewService(db *pgxpool.Pool, mailer email.Sender, frontendURL string) *Service {
	return &Service{db: db, mailer: mailer, frontendURL: frontendURL, ttl: 48 * time.Hour}
}

// Issue creates a token and sends the verification email.
// Rate-limited to 1 per 60 seconds per user.
func (s *Service) Issue(ctx context.Context, uid int64, recipient string) error {
	if err := s.checkThrottle(ctx, uid); err != nil {
		return err
	}
	tok := newToken()
	if _, err := s.db.Exec(ctx, `
        INSERT INTO email_verifications (user_id, token, expires_at)
        VALUES ($1, $2, $3)
    `, uid, tok, time.Now().Add(s.ttl)); err != nil {
		return fmt.Errorf("insert verification token:\n%w", err)
	}
	if err := s.touchThrottle(ctx, uid); err != nil {
		return err
	}
	link := s.frontendURL + "/verify-email?token=" + tok
	return s.mailer.Send(recipient, "Verify your email",
		"Click to verify your StudBud account: "+link)
}

// Verify consumes a token and flips users.email_verified.
func (s *Service) Verify(ctx context.Context, token string) error {
	uid, err := s.lookupToken(ctx, token)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx:\n%w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE users SET email_verified = true, verified_at = now(), updated_at = now() WHERE id = $1`, uid); err != nil {
		return fmt.Errorf("mark verified:\n%w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE email_verifications SET used_at = now() WHERE token = $1`, token); err != nil {
		return fmt.Errorf("mark token used:\n%w", err)
	}
	return tx.Commit(ctx)
}

// lookupToken fetches the user_id for a token and validates it is unused and unexpired.
func (s *Service) lookupToken(ctx context.Context, token string) (int64, error) {
	var uid int64
	var expires time.Time
	var used *time.Time
	err := s.db.QueryRow(ctx, `
        SELECT user_id, expires_at, used_at FROM email_verifications WHERE token = $1
    `, token).Scan(&uid, &expires, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("unknown token:\n%w", myErrors.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("load token:\n%w", err)
	}
	if used != nil {
		return 0, fmt.Errorf("token already used:\n%w", myErrors.ErrAlreadyVerified)
	}
	if time.Now().After(expires) {
		return 0, fmt.Errorf("token expired:\n%w", myErrors.ErrValidation)
	}
	return uid, nil
}

func (s *Service) checkThrottle(ctx context.Context, uid int64) error {
	var last time.Time
	err := s.db.QueryRow(ctx,
		`SELECT last_sent FROM email_verification_throttle WHERE user_id = $1`, uid).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load throttle:\n%w", err)
	}
	if time.Since(last) < 60*time.Second {
		return fmt.Errorf("rate limit: wait 60s between sends:\n%w", myErrors.ErrValidation)
	}
	return nil
}

func (s *Service) touchThrottle(ctx context.Context, uid int64) error {
	_, err := s.db.Exec(ctx, `
        INSERT INTO email_verification_throttle (user_id, last_sent, send_count)
        VALUES ($1, now(), 1)
        ON CONFLICT (user_id) DO UPDATE
          SET last_sent = EXCLUDED.last_sent,
              send_count = email_verification_throttle.send_count + 1
    `, uid)
	if err != nil {
		return fmt.Errorf("touch throttle:\n%w", err)
	}
	return nil
}

func newToken() string {
	var buf [24]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
