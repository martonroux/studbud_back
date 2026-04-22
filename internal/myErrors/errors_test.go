package myErrors

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrNotFound, ErrConflict) {
		t.Fatal("ErrNotFound and ErrConflict should be distinct")
	}
}

func TestAppErrorUnwrapsToSentinel(t *testing.T) {
	e := &AppError{Code: "quota_exhausted", Message: "hit cap", Wrapped: ErrQuotaExhausted}
	if !errors.Is(e, ErrQuotaExhausted) {
		t.Fatal("AppError should unwrap to its Wrapped sentinel")
	}
}

func TestAppErrorPreservesMessage(t *testing.T) {
	e := &AppError{Code: "validation", Message: "name missing", Field: "name", Wrapped: ErrValidation}
	if e.Error() == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestWrapMakesSentinelMatchable(t *testing.T) {
	wrapped := fmt.Errorf("loading subject 42:\n%w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatal("fmt.Errorf with %w should preserve sentinel match")
	}
}
