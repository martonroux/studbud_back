package testutil

import (
	"context"

	"studbud/backend/internal/billing"
)

// FakeBilling is a test double for billing.Client.
type FakeBilling struct {
	CheckoutURL string
	PortalURL   string
	WebhookErr  error
}

// CreateCheckout returns a canned URL.
func (f *FakeBilling) CreateCheckout(ctx context.Context, uid int64, priceID string) (*billing.CheckoutSession, error) {
	return &billing.CheckoutSession{URL: f.CheckoutURL, ID: "cs_test"}, nil
}

// CreatePortal returns a canned URL.
func (f *FakeBilling) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return f.PortalURL, nil
}

// VerifyWebhook returns the configured error (nil means valid).
func (f *FakeBilling) VerifyWebhook(payload []byte, signature string) error {
	return f.WebhookErr
}
