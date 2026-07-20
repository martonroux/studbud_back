package subject_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/subject"
	"studbud/backend/testutil"
)

func TestSubjectCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)
	owner := testutil.NewVerifiedUser(t, db)

	created, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Biology", Color: "#0a0"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.OwnerID != owner.ID || created.Name != "Biology" || created.Visibility != "private" {
		t.Fatalf("unexpected created subject: %+v", created)
	}

	got, err := svc.Get(ctx, owner.ID, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	newName := "Biology 101"
	updated, err := svc.Update(ctx, owner.ID, created.ID, subject.UpdateInput{Name: &newName})
	if err != nil || updated.Name != newName {
		t.Fatalf("update: %v %+v", err, updated)
	}

	list, err := svc.ListOwned(ctx, owner.ID, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err := svc.Delete(ctx, owner.ID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSubjectGet_ForbiddenForPrivate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)

	sub, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other.ID, sub.ID); err == nil {
		t.Fatal("expected forbidden for other user on private subject")
	}
}

// TestSubjectGet_NotFoundDoesNotLeakExistence is a regression test for SL-6: a
// private subject the caller has no relationship to must return the exact same
// error (ErrNotFound / 404) as a subject ID that does not exist at all, so the
// status code can't be used to probe for the existence of private subject IDs.
func TestSubjectGet_NotFoundDoesNotLeakExistence(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)

	sub, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Secret"})
	if err != nil {
		t.Fatal(err)
	}

	_, errPrivate := svc.Get(ctx, other.ID, sub.ID)
	if !errors.Is(errPrivate, myErrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for private subject caller can't see, got %v", errPrivate)
	}

	const nonexistentID = 987654321
	_, errMissing := svc.Get(ctx, other.ID, nonexistentID)
	if !errors.Is(errMissing, myErrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for nonexistent subject, got %v", errMissing)
	}
}
