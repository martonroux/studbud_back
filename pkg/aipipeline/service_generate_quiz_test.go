package aipipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestGenerateQuiz_StreamsQuestionsAndDebitsQuota(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"What is X?","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[]}]}`},
			{Done: true},
		},
	}
	svc := aipipeline.NewService(pool, fake, access.NewService(pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")

	out, err := svc.GenerateQuiz(context.Background(), aipipeline.QuizGenerateInput{
		UserID:    u.ID,
		SubjectID: sub.ID,
		Prompt:    "rendered body",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var items []json.RawMessage
	for chunk := range out.Chunks {
		if chunk.Kind == aipipeline.ChunkItem {
			items = append(items, chunk.Item)
		}
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT quiz_calls FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE`, u.ID,
	).Scan(&n)
	if n != 1 {
		t.Fatalf("quiz_calls = %d, want 1", n)
	}
}

func TestGenerateQuiz_RejectsWithoutAIAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")

	fake := &testutil.FakeAIClient{}
	svc := aipipeline.NewService(pool, fake, access.NewService(pool),
		aipipeline.DefaultQuotaLimits(), "claude-test")

	_, err := svc.GenerateQuiz(context.Background(), aipipeline.QuizGenerateInput{
		UserID: u.ID, SubjectID: sub.ID, Prompt: "x",
	})
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("want ErrNoAIAccess, got %v", err)
	}
}
