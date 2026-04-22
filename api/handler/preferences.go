package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/preferences"
)

// PreferencesHandler exposes get + update endpoints.
type PreferencesHandler struct {
	svc *preferences.Service // svc owns prefs logic
}

// NewPreferencesHandler constructs a PreferencesHandler.
func NewPreferencesHandler(svc *preferences.Service) *PreferencesHandler {
	return &PreferencesHandler{svc: svc}
}

// Get handles GET /preferences.
func (h *PreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	p, err := h.svc.Get(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// Update handles POST /preferences-update.
func (h *PreferencesHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in preferences.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Update(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
