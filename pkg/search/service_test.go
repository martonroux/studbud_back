package search_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/search"
	"studbud/backend/testutil"
)

func TestSearchSubjects_OwnedAndPublic(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	_ = testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry", "private")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Public", "public")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Secret", "private")

	hits, err := svc.Subjects(ctx, alice.ID, "chemistry", 10)
	if err != nil {
		t.Fatal(err)
	}
	// alice sees her own + bob's public, but not bob's private
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}
}

func TestSearchFlashcards_ScopedToViewableSubjects(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	aliceSubj := testutil.NewSubjectNamed(t, db, alice.ID, "A", "private")
	bobPublic := testutil.NewSubjectNamed(t, db, bob.ID, "BP", "public")
	bobPrivate := testutil.NewSubjectNamed(t, db, bob.ID, "BS", "private")
	testutil.NewFlashcard(t, db, aliceSubj.ID, 0, "What is mitochondria?", "powerhouse")
	testutil.NewFlashcard(t, db, bobPublic.ID, 0, "Define mitochondria", "the organelle")
	testutil.NewFlashcard(t, db, bobPrivate.ID, 0, "mitochondria secret", "hidden")

	hits, err := svc.Flashcards(ctx, alice.ID, "mitochondria", 10)
	if err != nil {
		t.Fatal(err)
	}
	// alice sees her own + bob's public; NOT bob's private
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}

	// Partial substring: "toch" must match "mitochondria"
	partial, err := svc.Flashcards(ctx, alice.ID, "toch", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 2 {
		t.Fatalf("partial 'toch' expected 2 hits, got %d: %+v", len(partial), partial)
	}
}

func TestSearchUsers(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	_ = testutil.NewVerifiedUserNamed(t, db, "alice_smith")
	_ = testutil.NewVerifiedUserNamed(t, db, "bob_jones")

	hits, err := svc.Users(ctx, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Username != "alice_smith" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
