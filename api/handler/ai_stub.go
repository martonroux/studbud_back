package handler

import (
	"encoding/json"
	"net/http"

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
