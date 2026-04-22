package jwt

import (
	"testing"
	"time"
)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	tok, err := s.Sign(Claims{UID: 42, EmailVerified: true})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UID != 42 || !got.EmailVerified {
		t.Errorf("claims = %+v, want UID=42 EmailVerified=true", got)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	tok, _ := s.Sign(Claims{UID: 1})
	_, err := s.Verify(tok + "x")
	if err == nil {
		t.Fatal("expected error on tampered token, got nil")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", -time.Hour)
	tok, _ := s.Sign(Claims{UID: 1})
	_, err := s.Verify(tok)
	if err == nil {
		t.Fatal("expected error on expired token, got nil")
	}
}
