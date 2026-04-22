package billing

import (
	"context"

	"studbud/backend/internal/myErrors"
)

// CheckoutSession is what the frontend redirects a user to.
type CheckoutSession struct {
	URL string
	ID  string
}

// Client is the billing-provider interface. Real Stripe impl arrives with Spec C.
type Client interface {
	CreateCheckout(ctx context.Context, uid int64, priceID string) (*CheckoutSession, error)
	CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error)
	VerifyWebhook(payload []byte, signature string) error
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

func (NoopClient) CreateCheckout(ctx context.Context, uid int64, priceID string) (*CheckoutSession, error) {
	return nil, myErrors.ErrNotImplemented
}

func (NoopClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

func (NoopClient) VerifyWebhook(payload []byte, signature string) error {
	return myErrors.ErrNotImplemented
}
