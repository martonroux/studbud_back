package handler

import (
	"io"
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/billing"
)

// BillingHandler stubs Spec C endpoints.
type BillingHandler struct {
	svc *billing.Service // svc is the (stub) billing service
}

// NewBillingHandler constructs a BillingHandler.
func NewBillingHandler(svc *billing.Service) *BillingHandler {
	return &BillingHandler{svc: svc}
}

// Checkout stubs POST /billing/checkout.
func (h *BillingHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Portal stubs POST /billing/portal.
func (h *BillingHandler) Portal(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Webhook stubs POST /billing/webhook (Stripe).
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
