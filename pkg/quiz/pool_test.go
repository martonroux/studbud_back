package quiz_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestResolveCardPool_Specific_All(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "What is X?", "Mitochondrion")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "What synth?", "Ribosomes")

	svc := quiz.NewService(pool, nil)
	cards, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sub.ID, Kind: quiz.KindSpecific, CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cards) != 2 || len(ids) != 2 {
		t.Fatalf("got %d cards / %d ids, want 2/2", len(cards), len(ids))
	}
}

func TestResolveCardPool_Global_Empty(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Hist", "private")

	svc := quiz.NewService(pool, nil)
	cards, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sub.ID, Kind: quiz.KindGlobal,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cards) != 0 || len(ids) != 0 {
		t.Fatalf("global kind should not materialise cards; got %d/%d", len(cards), len(ids))
	}
}

func TestResolveCardPool_ChapterScoped(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	c2 := testutil.NewChapter(t, pool, sub.ID, "C2")
	testutil.NewFlashcard(t, pool, sub.ID, c1, "qx", "ax")
	testutil.NewFlashcard(t, pool, sub.ID, c2, "qy", "ay")

	svc := quiz.NewService(pool, nil)
	_, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sub.ID, ChapterID: &c1,
		Kind: quiz.KindSpecific, CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("chapter scoping returned %d cards, want 1", len(ids))
	}
}
