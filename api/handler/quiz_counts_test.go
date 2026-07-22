package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/internal/authctx"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestGetQuizzesCardCounts_SubjectScope(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "q1", "a1")

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	req := httptest.NewRequest("GET", "/quizzes/card-counts?subjectId="+fmt.Sprintf("%d", sub.ID), nil)
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.CardCounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Counts   quiz.CardCounts          `json:"counts"`
		Chapters []quiz.ChapterCardCounts `json:"chapters"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if out.Counts.All != 1 {
		t.Fatalf("counts.all = %d, want 1", out.Counts.All)
	}
	if len(out.Chapters) != 1 || out.Chapters[0].ChapterID != c1 {
		t.Fatalf("chapters = %+v, want one entry for c1=%d", out.Chapters, c1)
	}
}

func TestGetQuizzesCardCounts_ChapterScope_OmitsChapters(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "q1", "a1")

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	req := httptest.NewRequest("GET",
		"/quizzes/card-counts?subjectId="+fmt.Sprintf("%d", sub.ID)+"&chapterId="+fmt.Sprintf("%d", c1), nil)
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.CardCounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := out["chapters"]; present {
		t.Fatalf("chapter-scoped response should omit `chapters`, got body=%s", w.Body.String())
	}
}

func TestGetQuizzesCardCounts_NotOwner_Rejected(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	intruder := testutil.NewVerifiedUserNamed(t, pool, "intruder")
	sub := testutil.NewSubjectNamed(t, pool, owner.ID, "Bio", "private")

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	req := httptest.NewRequest("GET", "/quizzes/card-counts?subjectId="+fmt.Sprintf("%d", sub.ID), nil)
	req = req.WithContext(authctx.WithIdentity(context.Background(), intruder.ID, true, false))
	w := httptest.NewRecorder()
	h.CardCounts(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want error for non-owner; body=%s", w.Body.String())
	}
}

func TestGetQuizzesCardCounts_MissingSubjectID_400(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	req := httptest.NewRequest("GET", "/quizzes/card-counts", nil)
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.CardCounts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
