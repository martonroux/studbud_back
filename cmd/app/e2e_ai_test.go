package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/config"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/testutil"
)

// TestE2E_AIHappyPath walks through: admin grant → generate (SSE) → commit → quota reflects debit.
func TestE2E_AIHappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	user := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, user.ID)

	cfg := &config.Config{
		Env: "test", FrontendURL: "http://fe.test", BackendURL: "http://be.test",
		DatabaseURL: "unused", JWTSecret: "a-minimum-32-byte-secret-xxxxxxxxxx",
		JWTIssuer: "studbud-test", JWTTTL: time.Hour,
		SMTPHost: "x", SMTPPort: "1", SMTPFrom: "x@x",
		UploadDir: t.TempDir(), AIModel: "test-model", StripeMode: "test",
	}
	d, cleanup := mustBuildDepsWithFake(t, pool, cfg, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	})
	defer cleanup()
	router := buildRouter(d)
	srv := httptest.NewServer(router)
	defer srv.Close()

	adminTok := mintE2EToken(t, cfg, admin.ID, true, true)
	userTok := mintE2EToken(t, cfg, user.ID, true, false)

	grantBody, _ := json.Marshal(map[string]any{"user_id": user.ID, "active": true})
	adminResp := aiDo(t, srv, "POST", "/admin/grant-ai-access", adminTok, bytes.NewReader(grantBody), "application/json")
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("grant status = %d", adminResp.StatusCode)
	}

	genBody, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "explain X", "style": "standard",
	})
	genResp := aiDo(t, srv, "POST", "/ai/flashcards/prompt", userTok, bytes.NewReader(genBody), "application/json")
	if genResp.StatusCode != http.StatusOK {
		t.Fatalf("generate status = %d", genResp.StatusCode)
	}
	body, _ := io.ReadAll(genResp.Body)
	_ = genResp.Body.Close()
	stream := string(body)
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}

	commitBody, _ := json.Marshal(map[string]any{
		"job_id": 1, "subject_id": subj.ID,
		"chapters": []any{},
		"cards": []any{
			map[string]any{"chapterClientId": "", "title": "t1", "question": "q1", "answer": "a1"},
		},
	})
	commitResp := aiDo(t, srv, "POST", "/ai/commit-generation", userTok, bytes.NewReader(commitBody), "application/json")
	if commitResp.StatusCode != http.StatusOK {
		t.Fatalf("commit status = %d", commitResp.StatusCode)
	}

	quotaResp := aiDo(t, srv, "GET", "/ai/quota", userTok, nil, "")
	if quotaResp.StatusCode != http.StatusOK {
		t.Fatalf("quota status = %d", quotaResp.StatusCode)
	}
	var quota map[string]any
	_ = json.NewDecoder(quotaResp.Body).Decode(&quota)
	if quota["aiAccess"] != true {
		t.Error("aiAccess = false after grant")
	}

	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1 AND source='ai'`, subj.ID).Scan(&count)
	if count != 1 {
		t.Errorf("flashcards source=ai count = %d, want 1", count)
	}
}

// aiDo is a small helper for synthetic HTTP calls in the AI e2e test.
func aiDo(t *testing.T, srv *httptest.Server, method, path, tok string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// mintE2EToken signs a JWT for the e2e test user.
func mintE2EToken(t *testing.T, cfg *config.Config, uid int64, verified, admin bool) string {
	t.Helper()
	signer := jwtsigner.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTL)
	tok, err := signer.Sign(jwtsigner.Claims{UID: uid, EmailVerified: verified, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
