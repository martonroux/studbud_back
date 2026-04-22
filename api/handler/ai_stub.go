package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// AIHandler exposes AI pipeline endpoints as stubs until Spec A ships.
type AIHandler struct {
	svc *aipipeline.Service // svc is the (stub) pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// GenerateFromPrompt is a stub for POST /ai/flashcards/prompt.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// GenerateFromPDF is a stub for POST /ai/flashcards/pdf.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Check is a stub for POST /ai/check.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
