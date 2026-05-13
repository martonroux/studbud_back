package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/quiz"
)

// QuizHandler exposes the Spec D quiz endpoints.
type QuizHandler struct {
	svc    *quiz.Service   // svc owns all quiz domain operations
	access *access.Service // access answers the AI-entitlement gate
}

// NewQuizHandler constructs a QuizHandler.
func NewQuizHandler(svc *quiz.Service, acc *access.Service) *QuizHandler {
	return &QuizHandler{svc: svc, access: acc}
}

// requireAIAccess is the entitlement check shared by generation endpoints.
// Plan D3 will extend this with the quizDemoUsed demo-path bypass.
func (h *QuizHandler) requireAIAccess(ctx context.Context, uid int64) error {
	ok, err := h.access.HasAIAccess(ctx, uid)
	if err != nil {
		return err
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// quizIDFromPath parses the {id} path value from the request.
// Handlers registered with stdlib mux's `/quizzes/{id}/...` shape pull it via r.PathValue.
func quizIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}

// attemptIDFromPath parses the {aid} path value from the request.
func attemptIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("aid")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}

// generateRequest is the JSON shape for POST /quizzes/generate.
type generateRequest struct {
	SubjectID  int64    `json:"subjectId"`
	ChapterID  *int64   `json:"chapterId,omitempty"`
	Kind       string   `json:"kind"`
	Size       int      `json:"size"`
	Types      []string `json:"types"`
	CardFilter string   `json:"cardFilter,omitempty"`
	// PlanContext is added in Plan D2.
}

// Generate handles POST /quizzes/generate.
func (h *QuizHandler) Generate(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if err := h.requireAIAccess(r.Context(), uid); err != nil {
		httpx.WriteError(w, err)
		return
	}

	var body generateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err))
		return
	}

	types := make([]quiz.QuestionType, 0, len(body.Types))
	for _, t := range body.Types {
		types = append(types, quiz.QuestionType(t))
	}
	req := quiz.GenerateRequest{
		UserID:     uid,
		SubjectID:  body.SubjectID,
		ChapterID:  body.ChapterID,
		Kind:       quiz.Kind(body.Kind),
		Size:       body.Size,
		Types:      types,
		CardFilter: quiz.CardFilter(body.CardFilter),
	}
	res, err := h.svc.Generate(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"quizId":        res.QuizID,
		"questionCount": res.QuestionCount,
		"kind":          res.Kind,
	})
}
