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

func TestCheck_ReturnsVerdictJSON(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"minor_issues","findings":[{"kind":"style","text":"tighten"}],"suggestion":{"title":"","question":"Q","answer":"A"}}`, Done: true},
		},
	}
	srv := newAICheckServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{"flashcard_id": fcID})
	req := httptest.NewRequest("POST", "/ai/check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["verdict"] != "minor_issues" {
		t.Errorf("verdict = %v, want minor_issues", resp["verdict"])
	}
}

func newAICheckServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/check", stack(http.HandlerFunc(h.Check)))
	return mux
}
