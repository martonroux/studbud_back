package handler

import (
	"context"
	"encoding/json"
	"errors"
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

// AIHandler exposes AI pipeline endpoints.
type AIHandler struct {
	svc *aipipeline.Service // svc is the AI pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// promptGenInput is the POST /ai/flashcards/prompt body.
type promptGenInput struct {
	SubjectID   int64  `json:"subject_id"`   // SubjectID is the target subject
	ChapterID   int64  `json:"chapter_id"`   // ChapterID is optional; when set, auto-chapters is suppressed
	Prompt      string `json:"prompt"`       // Prompt is the user's topic description
	TargetCount int    `json:"target_count"` // TargetCount is 0 (auto) or 1..50
	Style       string `json:"style"`        // Style is "short" | "standard" | "detailed"
	Focus       string `json:"focus"`        // Focus is an optional narrowing phrase
}

// GenerateFromPrompt is the SSE endpoint for prompt-based flashcard generation.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodePromptGen(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := renderPromptGenPromptExported(in, subject.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runGeneration(r.Context(), w, aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPrompt,
		SubjectID: in.SubjectID,
		Prompt:    rendered,
		Schema:    defaultItemsSchema(),
		Metadata: map[string]any{
			"style": in.Style, "focus": in.Focus, "target_count": in.TargetCount, "chapter_id": in.ChapterID,
		},
	})
}

// decodePromptGen parses + validates the prompt-gen body.
func decodePromptGen(r *http.Request) (promptGenInput, error) {
	var in promptGenInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || in.Prompt == "" || len(in.Prompt) > 8000 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id + prompt (<=8000 chars) required", Wrapped: myErrors.ErrValidation}
	}
	if in.Style == "" {
		in.Style = "standard"
	}
	return in, nil
}

// renderPromptGenPromptExported is the package-external renderer used by the handler.
func renderPromptGenPromptExported(in promptGenInput, subjectName string) (string, error) {
	return aipipeline.RenderPromptGen(aipipeline.PromptGenValues{
		SubjectName: subjectName,
		Target:      in.TargetCount,
		Style:       in.Style,
		Focus:       in.Focus,
		Prompt:      in.Prompt,
	})
}

// defaultItemsSchema returns the tool-use schema for a flashcard items array.
func defaultItemsSchema() []byte {
	return []byte(`{
      "type": "object",
      "properties": {
        "items": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "title":    {"type": "string"},
              "question": {"type": "string"},
              "answer":   {"type": "string"}
            },
            "required": ["question", "answer"]
          }
        }
      },
      "required": ["items"]
    }`)
}

// runGeneration invokes the pipeline and writes SSE events per emitted chunk.
// First event is always job; then card / chapter / progress / terminal done / error.
func (h *AIHandler) runGeneration(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
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

// setSSEHeaders writes the standard SSE content-type / cache headers.
func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}

// writeSSE writes one named SSE event with JSON-serialized data.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if flusher != nil {
		flusher.Flush()
	}
}

// forwardChunkToSSE maps an AIChunk to a named SSE event.
func forwardChunkToSSE(w http.ResponseWriter, flusher http.Flusher, c aipipeline.AIChunk) {
	switch c.Kind {
	case aipipeline.ChunkItem:
		writeSSE(w, flusher, "card", json.RawMessage(c.Item))
	case aipipeline.ChunkProgress:
		writeSSE(w, flusher, "progress", c.Progress)
	case aipipeline.ChunkDone:
		writeSSE(w, flusher, "done", map[string]any{"ok": true})
	case aipipeline.ChunkError:
		writeSSE(w, flusher, "error", errorPayload(c.Err))
	}
}

// errorPayload renders the JSON body for an SSE error event.
func errorPayload(err error) map[string]any {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return map[string]any{"code": ae.Code, "message": ae.Message}
	}
	return map[string]any{"code": "internal", "message": err.Error()}
}

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

// Check stubs POST /ai/check until Task 18.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Quota returns the authenticated user's current AI quota snapshot.
func (h *AIHandler) Quota(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	snap, err := h.svc.QuotaSnapshot(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
}

// commitInput is the POST /ai/commit-generation request body.
type commitInput struct {
	JobID     int64             `json:"job_id"`
	SubjectID int64             `json:"subject_id"`
	Chapters  []commitChapterIn `json:"chapters"`
	Cards     []commitCardIn    `json:"cards"`
}

type commitChapterIn struct {
	ClientID string `json:"clientId"`
	Title    string `json:"title"`
}

type commitCardIn struct {
	ChapterClientID string `json:"chapterClientId"`
	Title           string `json:"title"`
	Question        string `json:"question"`
	Answer          string `json:"answer"`
}

type commitOutput struct {
	SubjectID  int64            `json:"subjectId"`
	ChapterIDs map[string]int64 `json:"chapterIds"`
	CardIDs    []int64          `json:"cardIds"`
}

// CommitGeneration writes the user-edited AI draft atomically.
func (h *AIHandler) CommitGeneration(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodeCommit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.CommitGeneration(r.Context(), aipipeline.CommitInput{
		UserID:    uid,
		SubjectID: in.SubjectID,
		Chapters:  convertCommitChapters(in.Chapters),
		Cards:     convertCommitCards(in.Cards),
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, commitOutput{
		SubjectID:  out.SubjectID,
		ChapterIDs: out.ChapterIDs,
		CardIDs:    out.CardIDs,
	})
}

// decodeCommit parses and validates the commit body.
func decodeCommit(r *http.Request) (commitInput, error) {
	var in commitInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || len(in.Cards) == 0 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id and at least one card required", Wrapped: myErrors.ErrValidation}
	}
	return in, nil
}

// convertCommitChapters maps the JSON request shape to the service input shape.
func convertCommitChapters(in []commitChapterIn) []aipipeline.CommitChapter {
	out := make([]aipipeline.CommitChapter, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitChapter{ClientID: c.ClientID, Title: c.Title}
	}
	return out
}

// convertCommitCards maps the JSON request shape to the service input shape.
func convertCommitCards(in []commitCardIn) []aipipeline.CommitCard {
	out := make([]aipipeline.CommitCard, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitCard{
			ChapterClientID: c.ChapterClientID,
			Title:           c.Title,
			Question:        c.Question,
			Answer:          c.Answer,
		}
	}
	return out
}
