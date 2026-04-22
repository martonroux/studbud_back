package billing

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	billingadapter "studbud/backend/internal/billing"
	"studbud/backend/internal/myErrors"
)

// Service wraps the billing provider (Stripe in prod, fake in tests).
// Spec C fills in the real flows.
type Service struct {
	db       *pgxpool.Pool         // db is the shared pool
	provider billingadapter.Client // provider is the underlying billing adapter
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, provider billingadapter.Client) *Service {
	return &Service{db: db, provider: provider}
}

// CreateCheckoutSession returns a URL the user must visit to pay.
// Stub: not implemented until Spec C.
func (s *Service) CreateCheckoutSession(ctx context.Context, uid int64, tier string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// CreatePortalSession returns a URL for the Stripe customer portal.
func (s *Service) CreatePortalSession(ctx context.Context, uid int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// HandleWebhook processes a Stripe webhook payload.
func (s *Service) HandleWebhook(ctx context.Context, signature string, body []byte) error {
	return myErrors.ErrNotImplemented
}
