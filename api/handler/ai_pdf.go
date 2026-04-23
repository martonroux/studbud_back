package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// pdfGenInput captures the form fields for POST /ai/flashcards/pdf.
type pdfGenInput struct {
	SubjectID    int64  // SubjectID is the target subject
	ChapterID    int64  // ChapterID is optional; when set, suppresses auto-chapters
	Coverage     string // Coverage is "essentials" | "balanced" | "comprehensive"
	Style        string // Style is "short" | "standard" | "detailed"
	Focus        string // Focus is an optional narrowing phrase
	AutoChapters bool   // AutoChapters requests proposed chapter splits
	PDFBytes     []byte // PDFBytes is the uploaded file
}

// GenerateFromPDF is the SSE endpoint for PDF-based flashcard generation.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := parsePDFForm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	images, err := rasterizePDF(r.Context(), in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := renderPDFPrompt(in, subject.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runPDFGeneration(r.Context(), w, uid, in, rendered, images)
}

// renderPDFPrompt renders the PDF-mode prompt template from form inputs.
func renderPDFPrompt(in pdfGenInput, subjectName string) (string, error) {
	return aipipeline.RenderPDFGen(aipipeline.PDFGenValues{
		SubjectName:  subjectName,
		Style:        in.Style,
		Coverage:     in.Coverage,
		CoverageHint: coverageHint(in.Coverage),
		Focus:        in.Focus,
		AutoChapters: in.AutoChapters && in.ChapterID == 0,
	})
}

// runPDFGeneration pushes the assembled request through the pipeline with images attached.
func (h *AIHandler) runPDFGeneration(
	ctx context.Context, w http.ResponseWriter,
	uid int64, in pdfGenInput, rendered string, images []aiProvider.ImagePart,
) {
	req := aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPDF,
		SubjectID: in.SubjectID,
		Prompt:    rendered,
		PDFBytes:  in.PDFBytes,
		PDFPages:  len(images),
		Images:    images,
		Schema:    defaultPDFItemsSchema(),
		Metadata: map[string]any{
			"coverage": in.Coverage, "style": in.Style, "focus": in.Focus,
			"auto_chapters": in.AutoChapters, "chapter_id": in.ChapterID,
			"page_count": len(images),
		},
	}
	h.runGenerationWithReq(ctx, w, req)
}

// runGenerationWithReq is the image-aware sibling of runGeneration.
// Identical shape; separate name for readability.
func (h *AIHandler) runGenerationWithReq(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
	ch, jobID, err := h.svc.RunStructuredGeneration(ctx, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	writeSSE(w, flusher, "job", map[string]any{"jobId": jobID})
	for c := range ch {
		forwardChunkToSSE(w, flusher, c)
	}
}

// parsePDFForm reads the multipart form and returns a validated pdfGenInput.
func parsePDFForm(r *http.Request) (pdfGenInput, error) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		return pdfGenInput{}, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return pdfGenInput{}, &myErrors.AppError{Code: "validation", Message: "file field required", Wrapped: myErrors.ErrValidation, Field: "file"}
	}
	defer f.Close()
	bytesBuf, err := readAllCapped(f, 20<<20)
	if err != nil {
		return pdfGenInput{}, err
	}
	return pdfGenInput{
		SubjectID:    parseInt64Form(r, "subject_id"),
		ChapterID:    parseInt64Form(r, "chapter_id"),
		Coverage:     orDefaultStr(r.FormValue("coverage"), "balanced"),
		Style:        orDefaultStr(r.FormValue("style"), "standard"),
		Focus:        r.FormValue("focus"),
		AutoChapters: r.FormValue("auto_chapters") == "true",
		PDFBytes:     bytesBuf,
	}, nil
}

// readAllCapped slurps at most limit bytes; returns pdf_too_large past that.
func readAllCapped(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read file:\n%w", err)
	}
	if int64(len(buf)) > limit {
		return nil, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	return buf, nil
}

// rasterizePDF turns a PDF byte slice into a per-page []ImagePart with a hard page cap of 30.
func rasterizePDF(ctx context.Context, pdfBytes []byte) ([]aiProvider.ImagePart, error) {
	imgs, err := aiProvider.PDFToImages(ctx, pdfBytes, aiProvider.PDFOptions{MaxPages: 30, PerPageTimeout: 30 * time.Second})
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	return imgs, nil
}

// coverageHint returns a short English hint for each coverage level.
func coverageHint(c string) string {
	switch c {
	case "essentials":
		return "cover only the most important 20%"
	case "comprehensive":
		return "cover everything substantive"
	default:
		return "cover the key 50%"
	}
}

// defaultPDFItemsSchema extends the default items schema with chapters.
func defaultPDFItemsSchema() []byte {
	return []byte(`{
      "type": "object",
      "properties": {
        "chapters": {
          "type": "array",
          "items": {"type": "object", "properties": {"index": {"type": "integer"}, "title": {"type": "string"}}}
        },
        "items": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "chapterIndex": {"type": ["integer","null"]},
              "title":    {"type": "string"},
              "question": {"type": "string"},
              "answer":   {"type": "string"}
            },
            "required": ["question","answer"]
          }
        }
      },
      "required": ["items"]
    }`)
}

// parseInt64Form parses a multipart form field into int64; 0 on absence/parse-error.
func parseInt64Form(r *http.Request, field string) int64 {
	var v int64
	_, _ = fmt.Sscanf(r.FormValue(field), "%d", &v)
	return v
}

// orDefaultStr returns s unless empty, in which case fallback.
func orDefaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
