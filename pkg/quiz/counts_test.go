package quiz_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestCardCounts_SubjectScope_WithChapterBreakdown(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	c2 := testutil.NewChapter(t, pool, sub.ID, "C2")

	fc1 := testutil.NewFlashcard(t, pool, sub.ID, c1, "q1", "a1") // new (-1), not due
	fc2 := testutil.NewFlashcard(t, pool, sub.ID, c1, "q2", "a2") // bad, overdue
	testutil.NewFlashcard(t, pool, sub.ID, c2, "q3", "a3")        // new (-1), not due
	_ = fc1

	if _, err := pool.Exec(context.Background(),
		`UPDATE flashcards SET last_result = 0, due_at = now() - interval '1 day' WHERE id = $1`, fc2,
	); err != nil {
		t.Fatalf("seed review state: %v", err)
	}

	svc := quiz.NewService(pool, nil)
	res, err := svc.CardCounts(context.Background(), quiz.CardCountsRequest{
		UserID: u.ID, SubjectID: sub.ID,
	})
	if err != nil {
		t.Fatalf("card counts: %v", err)
	}
	if res.Counts != (quiz.CardCounts{All: 3, BadOK: 1, Due: 1}) {
		t.Fatalf("subject counts = %+v, want {3,1,1}", res.Counts)
	}
	if len(res.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(res.Chapters))
	}
	byID := map[int64]quiz.ChapterCardCounts{}
	for _, c := range res.Chapters {
		byID[c.ChapterID] = c
	}
	if got := byID[c1].Counts; got != (quiz.CardCounts{All: 2, BadOK: 1, Due: 1}) {
		t.Fatalf("c1 counts = %+v, want {2,1,1}", got)
	}
	if got := byID[c2].Counts; got != (quiz.CardCounts{All: 1, BadOK: 0, Due: 0}) {
		t.Fatalf("c2 counts = %+v, want {1,0,0}", got)
	}
	if byID[c1].Title != "C1" || byID[c2].Title != "C2" {
		t.Fatalf("chapter titles missing: %+v", res.Chapters)
	}
}

func TestCardCounts_ChapterScope_NoBreakdown(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	c2 := testutil.NewChapter(t, pool, sub.ID, "C2")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "q1", "a1")
	testutil.NewFlashcard(t, pool, sub.ID, c2, "q2", "a2")

	svc := quiz.NewService(pool, nil)
	res, err := svc.CardCounts(context.Background(), quiz.CardCountsRequest{
		UserID: u.ID, SubjectID: sub.ID, ChapterID: &c1,
	})
	if err != nil {
		t.Fatalf("card counts: %v", err)
	}
	if res.Counts != (quiz.CardCounts{All: 1, BadOK: 0, Due: 0}) {
		t.Fatalf("chapter counts = %+v, want {1,0,0}", res.Counts)
	}
	if res.Chapters != nil {
		t.Fatalf("chapter-scoped result should not include chapters breakdown, got %+v", res.Chapters)
	}
}

func TestCardCounts_ChaptersWithZeroCards_Included(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	testutil.NewChapter(t, pool, sub.ID, "Empty")

	svc := quiz.NewService(pool, nil)
	res, err := svc.CardCounts(context.Background(), quiz.CardCountsRequest{
		UserID: u.ID, SubjectID: sub.ID,
	})
	if err != nil {
		t.Fatalf("card counts: %v", err)
	}
	if len(res.Chapters) != 1 || res.Chapters[0].Counts.All != 0 {
		t.Fatalf("expected one zero-card chapter, got %+v", res.Chapters)
	}
}

func TestCardCounts_NotOwner_Rejected(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	intruder := testutil.NewVerifiedUserNamed(t, pool, "intruder")
	sub := testutil.NewSubjectNamed(t, pool, owner.ID, "Bio", "private")

	svc := quiz.NewService(pool, nil)
	_, err := svc.CardCounts(context.Background(), quiz.CardCountsRequest{
		UserID: intruder.ID, SubjectID: sub.ID,
	})
	if err == nil {
		t.Fatal("want error for non-owner, got nil")
	}
}

func TestCardCounts_DueFilter_ExcludesFutureDue(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	future := testutil.NewFlashcard(t, pool, sub.ID, 0, "q1", "a1")
	past := testutil.NewFlashcard(t, pool, sub.ID, 0, "q2", "a2")

	if _, err := pool.Exec(context.Background(),
		`UPDATE flashcards SET due_at = $2 WHERE id = $1`, future, time.Now().Add(48*time.Hour),
	); err != nil {
		t.Fatalf("seed future due_at: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE flashcards SET due_at = $2 WHERE id = $1`, past, time.Now().Add(-48*time.Hour),
	); err != nil {
		t.Fatalf("seed past due_at: %v", err)
	}

	svc := quiz.NewService(pool, nil)
	res, err := svc.CardCounts(context.Background(), quiz.CardCountsRequest{
		UserID: u.ID, SubjectID: sub.ID,
	})
	if err != nil {
		t.Fatalf("card counts: %v", err)
	}
	if res.Counts.Due != 1 {
		t.Fatalf("due = %d, want 1 (only the past-due card)", res.Counts.Due)
	}
}
