package aipipeline_test

import (
	"context"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestCheckFlashcard_ReturnsVerdictAndSuggestion(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q1", "A1")

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"ok","findings":[],"suggestion":{"title":"","question":"Q1","answer":"A1"}}`, Done: true},
		},
	}
	svc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")

	out, err := svc.CheckFlashcard(context.Background(), aipipeline.CheckInput{
		UserID:      u.ID,
		FlashcardID: fcID,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.Verdict != "ok" {
		t.Errorf("Verdict = %q, want ok", out.Verdict)
	}
	if out.Suggestion.Question != "Q1" {
		t.Errorf("Suggestion.Question = %q, want Q1", out.Suggestion.Question)
	}
	if out.JobID <= 0 {
		t.Errorf("JobID = %d, want > 0", out.JobID)
	}
}
