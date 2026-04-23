package aipipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestRun_RejectsWhenNoAIAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("err = %v, want ErrNoAIAccess", err)
	}
}

func TestRun_RejectsWhenQuotaExhausted(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 20)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
}

func TestRun_RejectsWhenConcurrentGenerationExists(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	existing := testutil.SeedRunningJob(t, pool, u.ID, "generate_prompt")

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	var ae *myErrors.AppError
	if !errors.As(err, &ae) || !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("err = %v, want AppError wrapping ErrConflict", err)
	}
	if ae.Code != "concurrent_generation" {
		t.Errorf("Code = %q, want concurrent_generation", ae.Code)
	}
	if !containsJobID(ae.Message, existing) {
		t.Errorf("Message = %q, expected to include jobID %d", ae.Message, existing)
	}
}

func TestRun_InsertsRunningJobBeforeStream(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Done: true}},
	})
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if jobID <= 0 {
		t.Errorf("jobID = %d, want > 0", jobID)
	}
	for range ch {
	}
	if n := testutil.CountAIJobs(t, pool, u.ID); n != 1 {
		t.Errorf("ai_jobs count = %d, want 1", n)
	}
}

func newPipelineSvc(pool *pgxpool.Pool, cli aiProvider.Client) *aipipeline.Service {
	return aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
}

func newPromptReq(uid, subjectID int64) aipipeline.AIRequest {
	return aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPrompt,
		SubjectID: subjectID,
		Prompt:    "anything",
		Schema:    json.RawMessage(`{"type":"object"}`),
		Metadata:  map[string]any{"style": "standard"},
	}
}

func containsJobID(s string, id int64) bool {
	return len(s) > 0 && (indexRune(s, '0'+int32(id%10)) >= 0 || indexByte(s, '#') >= 0)
}

func indexRune(s string, r int32) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
