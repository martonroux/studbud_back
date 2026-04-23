package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestCommitGeneration_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAICommitServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"job_id":     1,
		"subject_id": subj.ID,
		"chapters":   []any{map[string]any{"clientId": "c1", "title": "Intro"}},
		"cards": []any{
			map[string]any{"chapterClientId": "c1", "title": "t", "question": "q", "answer": "a"},
		},
	})
	req := httptest.NewRequest("POST", "/ai/commit-generation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func newAICommitServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, aiProvider.NoopClient{}, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/commit-generation", stack(http.HandlerFunc(h.CommitGeneration)))
	return mux
}
