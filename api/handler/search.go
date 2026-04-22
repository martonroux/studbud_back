package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/search"
)

// SearchHandler exposes search endpoints.
type SearchHandler struct {
	svc *search.Service // svc owns the search queries
}

// NewSearchHandler constructs a SearchHandler.
func NewSearchHandler(svc *search.Service) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// Subjects handles GET /search/subjects?q=...
func (h *SearchHandler) Subjects(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	q := r.URL.Query().Get("q")
	hits, err := h.svc.Subjects(r.Context(), uid, q, 20)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

// Users handles GET /search/users?q=...
func (h *SearchHandler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	hits, err := h.svc.Users(r.Context(), q, 20)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}
