package handler

import (
	"strings"
	"testing"
)

func TestAppendDocumentText_IncludesPageMarkers(t *testing.T) {
	got := appendDocumentText("PROMPT", []string{"alpha", "beta"})
	for _, want := range []string{"PROMPT", "--- Document text ---", "--- Page 1 ---", "alpha", "--- Page 2 ---", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
