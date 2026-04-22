package friendship_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/friendship"
	"studbud/backend/testutil"
)

// TestFriendshipFlow exercises the happy path: request, receiver accepts, list, unfriend.
func TestFriendshipFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	f, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if f.Status != "pending" {
		t.Fatalf("expected pending, got %s", f.Status)
	}

	if _, err := svc.Accept(ctx, alice.ID, f.ID); err == nil {
		t.Fatal("sender should not be able to accept")
	}

	acc, err := svc.Accept(ctx, bob.ID, f.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.Status != "accepted" {
		t.Fatalf("expected accepted, got %s", acc.Status)
	}

	friends, err := svc.ListFriends(ctx, alice.ID)
	if err != nil {
		t.Fatalf("list friends: %v", err)
	}
	if len(friends) != 1 {
		t.Fatalf("expected 1 friend, got %d", len(friends))
	}

	if err := svc.Unfriend(ctx, alice.ID, f.ID); err != nil {
		t.Fatalf("unfriend: %v", err)
	}
}

// TestFriendshipRequest_DuplicateRejected verifies unique-violation maps to ErrConflict.
func TestFriendshipRequest_DuplicateRejected(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	if _, err := svc.Request(ctx, alice.ID, bob.ID); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := svc.Request(ctx, alice.ID, bob.ID)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}
