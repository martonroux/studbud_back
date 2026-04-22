package user

import (
	"context"
	"errors"
	"testing"
	"time"

	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/myErrors"
	"studbud/backend/testutil"
)

func TestRegisterAndLogin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	svc := NewService(pool, signer)

	tok, uid, err := svc.Register(context.Background(), RegisterInput{
		Username: "alice", Email: "alice@example.com", Password: "password123",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tok == "" || uid == 0 {
		t.Fatal("empty token or uid")
	}

	tok2, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok2 == "" {
		t.Fatal("empty login token")
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))

	_, _, err := svc.Register(context.Background(), RegisterInput{Username: "a", Email: "a@x.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Register(context.Background(), RegisterInput{Username: "a", Email: "b@x.com", Password: "password123"})
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))
	_, _, _ = svc.Register(context.Background(), RegisterInput{Username: "alice", Email: "alice@x.com", Password: "password123"})

	_, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "wrongpass"})
	if !errors.Is(err, myErrors.ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}
