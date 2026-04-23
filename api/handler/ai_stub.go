package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// AIHandler exposes AI pipeline endpoints.
type AIHandler struct {
	svc *aipipeline.Service // svc is the AI pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// GenerateFromPrompt stubs POST /ai/flashcards/prompt until Task 16.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// GenerateFromPDF stubs POST /ai/flashcards/pdf until Task 17.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Check stubs POST /ai/check until Task 18.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
