package aipipeline_test

import (
	"testing"

	"studbud/backend/pkg/aipipeline"
)

func TestFeatureGenerateQuiz_StringValue(t *testing.T) {
	if got, want := string(aipipeline.FeatureGenerateQuiz), "generate_quiz"; got != want {
		t.Fatalf("FeatureGenerateQuiz = %q, want %q", got, want)
	}
}

func TestDefaultQuotaLimits_HasQuizCalls(t *testing.T) {
	lim := aipipeline.DefaultQuotaLimits()
	if lim.QuizCalls <= 0 {
		t.Fatalf("DefaultQuotaLimits().QuizCalls = %d, want > 0", lim.QuizCalls)
	}
}
