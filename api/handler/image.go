package handler

import (
	"io"
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/image"
)

// ImageHandler handles upload / serve / delete.
type ImageHandler struct {
	svc *image.Service // svc is the image domain service
}

// NewImageHandler constructs the handler.
func NewImageHandler(svc *image.Service) *ImageHandler {
	return &ImageHandler{svc: svc}
}

// Upload handles POST /upload-image (multipart).
// Hard-caps the request body at 6 MiB to prevent unbounded disk usage from form spillover.
func (h *ImageHandler) Upload(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	defer file.Close()
	img, err := h.svc.Upload(r.Context(), uid, file, hdr.Filename)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": img.ID, "url": h.svc.URL(img.ID)})
}

// Serve handles GET /images/{id}.
func (h *ImageHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rc, mime, err := h.svc.Open(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, rc)
}

// Delete handles POST /delete-image?id=...
func (h *ImageHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id := r.URL.Query().Get("id")
	if id == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
