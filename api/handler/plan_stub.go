package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/plan"
)

// PlanHandler stubs Spec B endpoints.
type PlanHandler struct {
	svc *plan.Service // svc is the (stub) plan service
}

// NewPlanHandler constructs a PlanHandler.
func NewPlanHandler(svc *plan.Service) *PlanHandler { return &PlanHandler{svc: svc} }

// Generate stubs POST /plan/generate.
func (h *PlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Progress stubs GET /plan/progress?id=...
func (h *PlanHandler) Progress(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
