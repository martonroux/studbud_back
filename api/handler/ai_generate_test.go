package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGenerateFromPrompt_StreamsJobThenCardsThenDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIGenServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "Explain photosynthesis", "style": "standard",
	})
	req := httptest.NewRequest("POST", "/ai/flashcards/prompt", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	stream := w.Body.String()
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}
}

func newAIGenServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/flashcards/prompt", stack(http.HandlerFunc(h.GenerateFromPrompt)))
	return mux
}
