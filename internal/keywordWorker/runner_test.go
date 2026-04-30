package keywordWorker

import (
	"context"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

// fakeProv emits a single fixed chunk on each Stream call.
type fakeProv struct {
	body string // body is the JSON payload returned in one Done chunk
}

// Stream returns a channel that yields one chunk and closes.
func (f *fakeProv) Stream(_ context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	ch := make(chan aiProvider.Chunk, 1)
	ch <- aiProvider.Chunk{Text: f.body, Done: true}
	close(ch)

	return ch, nil
}

func TestRunOnce_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Phases?", "Pro/meta/ana/telo.")

	if err := NewEnqueuer(pool).EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProv{body: `{"keywords":[{"keyword":"mitose","weight":1.0},{"keyword":"phase","weight":0.6}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")

	r := &Runner{db: pool, ai: ai}

	n, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n != 1 {
		t.Fatalf("want 1 job processed, got %d", n)
	}

	var state string

	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&state); err != nil {
		t.Fatal(err)
	}

	if state != "done" {
		t.Errorf("want done, got %q", state)
	}

	var count int

	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM flashcard_keywords WHERE fc_id=$1`, fcID).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Errorf("want 2 keywords stored, got %d", count)
	}
}

func TestRunOnce_EmptyAfterCleanupMarksFailed(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	if err := NewEnqueuer(pool).EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProv{body: `{"keywords":[{"keyword":"   ","weight":0.5}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")

	r := &Runner{db: pool, ai: ai}

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	var state, lastErr string

	if err := pool.QueryRow(context.Background(),
		`SELECT state, COALESCE(last_error,'') FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}

	if state != "failed" || lastErr != "empty_after_cleanup" {
		t.Errorf("want failed/empty_after_cleanup, got %s/%s", state, lastErr)
	}
}
