package httpx

import (
	"net/http/httptest"
	"testing"

	"studbud/backend/internal/myErrors"
)

func TestQueryOptionalInt64_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	v, err := QueryOptionalInt64(r, "chapterId")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if v != nil {
		t.Fatalf("v = %v, want nil", v)
	}
}

func TestQueryOptionalInt64_Present(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?chapterId=42", nil)
	v, err := QueryOptionalInt64(r, "chapterId")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if v == nil || *v != 42 {
		t.Fatalf("v = %v, want 42", v)
	}
}

func TestQueryOptionalInt64_Unparseable(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?chapterId=abc", nil)
	_, err := QueryOptionalInt64(r, "chapterId")
	if err != myErrors.ErrInvalidInput {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
