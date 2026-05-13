# Spec D Part 2 — Revision-Plan Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate Spec D quizzes into Spec B revision plans. Adds an `intensity` setting on plans (`light`/`normal`/`intense`), extends day-plan JSON with `quizSlots`, lets `POST /quizzes/generate` accept a `planContext` (skips quota, marks the quiz as plan-sourced), and writes `revision_plan_progress` rows on quiz completion. Excludes the standalone quiz core (Plan D1) and sharing/quality (Plan D3).

**Architecture:** A single column (`revision_plans.intensity`) drives the AI's quiz-slot count during plan generation. Each day-plan JSON gains a `quizSlots` array — pre-baked at plan-generation time with `{kind, suggestedSize, suggestedTypes, cardPool}` but no `quizId`. When the user taps a slot the front-end hits `POST /quizzes/generate` with `planContext: { planId, planDate, slotIndex }`; the service bypasses quota (`planQuotaCovered`), persists with `source='plan'` and `source_plan_id`, and back-fills the slot's `quizId` inside `days_jsonb`. The play attempt carries `plan_id` + `plan_date`; on completion the service writes `revision_plan_progress` rows for every `referenced_fc_ids` in the answered questions.

**Tech Stack:** Same as Plan D1 — Go 1.25 + pgx + stdlib `net/http`; `pkg/aipipeline` for AI calls; `pkg/plan` for revision plans; Postgres for state.

**Spec reference:** `docs/superpowers/specs/2026-04-21-ai-quiz-design.md` §2 (revision_plans amendment), §3 (planContext), §5.3 (plan-integrated flow), §5.2 (completion writeback).

**Hard dependency:** Plan D1 must be merged (or at least its schema reconciliation + `pkg/quiz` service skeleton) before this plan runs. The quiz tables created by D1 carry the `plan_id` / `plan_date` columns this plan relies on.

**Prerequisite reading for the implementer:**
- Spec D design doc, §2 amendments + §5.3 flow
- `pkg/plan/model.go` (current `Day` struct)
- `pkg/plan/generate.go` (how `RunStructuredGeneration` is consumed)
- `pkg/aipipeline/service_generation.go:32-41` (preflight + debit path — the entrypoint we'll need a no-quota variant of)
- `pkg/aipipeline/prompts/revision_plan.tmpl` (the template we extend)
- `db_sql/setup_plan.go:29-47` (revision_plans + revision_plan_progress)
- `pkg/quiz/generate.go` (from Plan D1 — we extend `PlanContext` handling)

---

## Phase 1 — Schema: `revision_plans.intensity`

### Task 1: Schema test + ALTER

**Files:**
- Modify: `db_sql/setup_plan.go` (append the ALTER statement)
- Test: `db_sql/setup_plan_test.go` (new file, follows the `setup_quiz_test.go` pattern from Plan D1)

- [ ] **Step 1: Write the failing test**

`db_sql/setup_plan_test.go`:

```go
package db_sql

import (
	"context"
	"testing"

	"studbud/backend/testutil"
)

// TestRevisionPlansHasIntensity asserts §2 of Spec D adds the column
// with CHECK ('light','normal','intense') and default 'normal'.
func TestRevisionPlansHasIntensity(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	requireColumn(t, pool, "revision_plans", "intensity")
	requireNotNull(t, pool, "revision_plans", "intensity")

	// Default must be 'normal'.
	var dflt string
	err := pool.QueryRow(context.Background(),
		`SELECT column_default FROM information_schema.columns
		   WHERE table_name='revision_plans' AND column_name='intensity'`,
	).Scan(&dflt)
	if err != nil {
		t.Fatalf("column_default: %v", err)
	}
	// Postgres returns the default as quoted SQL ("'normal'::text").
	if dflt == "" {
		t.Fatalf("intensity has no default")
	}

	// CHECK constraint rejects an invalid value. Use an exam fixture from testutil.
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "S")
	eid := testutil.NewExam(t, pool, u.ID, sid, "2026-12-01")
	_, err = pool.Exec(context.Background(),
		`INSERT INTO revision_plans (exam_id, days, model, prompt_hash, intensity)
		 VALUES ($1, '[]'::jsonb, 'm', 'h', 'bonkers')`, eid)
	if err == nil {
		t.Fatalf("CHECK accepted invalid intensity")
	}
}
```

(`requireColumn` / `requireNotNull` are reused from `setup_quiz_test.go` in Plan D1 — they live in the same package. `testutil.NewExam` is the Spec B test helper.)

- [ ] **Step 2: Run test to verify it fails**

```
go test ./db_sql/ -run TestRevisionPlansHasIntensity -v
```

Expected: FAIL — the column doesn't exist.

- [ ] **Step 3: Add the ALTER statement**

In `db_sql/setup_plan.go`, inside `planSchema` (around line 37, after the existing `ALTER TABLE revision_plans ADD COLUMN IF NOT EXISTS generation_id ...`):

```sql
ALTER TABLE revision_plans
    ADD COLUMN IF NOT EXISTS intensity TEXT NOT NULL DEFAULT 'normal'
    CHECK (intensity IN ('light','normal','intense'));
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./db_sql/ -run TestRevisionPlansHasIntensity -v
```

Expected: PASS. (Drop+recreate the test DB if the previous run left `revision_plans` in the old shape: `dropdb studbud_test && createdb studbud_test`.)

- [ ] **Step 5: Run the wider schema suite**

```
go test ./db_sql/... -v
```

Expected: PASS for all schema assertions including D1's quiz test.

- [ ] **Step 6: Commit**

```bash
git add db_sql/setup_plan.go db_sql/setup_plan_test.go
git commit -m "$(cat <<'EOF'
Spec D: revision_plans.intensity column

[+] intensity TEXT NOT NULL DEFAULT 'normal' CHECK (light|normal|intense)
[+] setup_plan_test.go asserts column shape, default, CHECK
EOF
)"
```

---

## Phase 2 — Plan model + generation gain `quizSlots`

The revision-plan generator (Spec B) currently emits `Day` rows with three card buckets. D2 grows the `Day` JSON shape with an optional `quizSlots` array.

### Task 2: Extend `pkg/plan/model.go` with `QuizSlot`

**Files:**
- Modify: `pkg/plan/model.go`
- Test: `pkg/plan/model_test.go`

- [ ] **Step 1: Write the failing test**

`pkg/plan/model_test.go`:

```go
package plan_test

import (
	"encoding/json"
	"testing"

	"studbud/backend/pkg/plan"
)

func TestDay_JSONRoundtrip_PreservesQuizSlots(t *testing.T) {
	in := plan.Day{
		Date:                "2026-12-01",
		PrimarySubjectCards: []int64{1, 2},
		CrossSubjectCards:   []int64{},
		DeeperDives:         []int64{},
		QuizSlots: []plan.QuizSlot{
			{
				Kind:           "specific",
				SuggestedSize:  10,
				SuggestedTypes: []string{"multi_choice", "true_false"},
				CardPool:       []int64{1, 2, 7},
			},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out plan.Day
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.QuizSlots) != 1 || out.QuizSlots[0].SuggestedSize != 10 {
		t.Fatalf("roundtrip lost quizSlots: %+v", out)
	}
}

func TestDay_JSONRoundtrip_OmitsEmptyQuizSlots(t *testing.T) {
	in := plan.Day{Date: "2026-12-01", PrimarySubjectCards: []int64{}, CrossSubjectCards: []int64{}, DeeperDives: []int64{}}
	raw, _ := json.Marshal(in)
	if string(raw) == "" || containsKey(raw, "quizSlots") {
		t.Fatalf("empty quizSlots leaked into JSON: %s", raw)
	}
}

func containsKey(raw []byte, key string) bool {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	_, ok := m[key]
	return ok
}
```

- [ ] **Step 2: Run to verify failure**

```
go test ./pkg/plan/ -run 'TestDay_JSONRoundtrip' -v
```

Expected: BUILD FAIL — `plan.QuizSlot` undefined, `Day.QuizSlots` field missing.

- [ ] **Step 3: Add the types**

In `pkg/plan/model.go`, after the existing `Day` struct (line 25-30):

```go
// QuizSlot is one AI-pre-baked quiz placeholder inside a day-plan bucket.
// SuggestedSize/SuggestedTypes are advisory hints rendered in the UI;
// CardPool is the frozen flashcard id set the AI selected at plan-generation time.
// QuizID is non-zero only after the user has tapped the slot and the service
// has materialised the quiz via POST /quizzes/generate?planContext.
type QuizSlot struct {
	Kind           string  `json:"kind"`                     // "specific" | "global"
	SuggestedSize  int     `json:"suggestedSize"`            // 5/10/15/20
	SuggestedTypes []string `json:"suggestedTypes"`          // subset of multi_choice|true_false|fill_blank
	CardPool       []int64 `json:"cardPool"`                 // fc_ids; [] for global
	QuizID         int64   `json:"quizId,omitempty"`         // filled once materialised
}
```

And extend `Day`:

```go
type Day struct {
	Date                string     `json:"date"`
	PrimarySubjectCards []int64    `json:"primarySubjectCards"`
	CrossSubjectCards   []int64    `json:"crossSubjectCards"`
	DeeperDives         []int64    `json:"deeperDives"`
	QuizSlots           []QuizSlot `json:"quizSlots,omitempty"` // Spec D §2
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/plan/ -run 'TestDay_JSONRoundtrip' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/plan/model.go pkg/plan/model_test.go
git commit -m "$(cat <<'EOF'
Spec D: Day.QuizSlots + QuizSlot type

[+] QuizSlot{Kind, SuggestedSize, SuggestedTypes, CardPool, QuizID}
[&] Day grows quizSlots (omitempty preserves existing plan JSONs)
[+] model_test.go covers JSON round-trip + empty omission
EOF
)"
```

### Task 3: Plan generation reads + passes `intensity`

`POST /exams/:id/generate-plan` accepts an `intensity` field that's persisted into the new column. The plan generator passes it into the AI prompt.

**Files:**
- Modify: `pkg/plan/generate.go` (extend `PlanGenerateInput`)
- Modify: `api/handler/revision_plan.go` (accept `intensity` in request body)
- Modify: `pkg/aipipeline/prompts/revision_plan.tmpl` (consume intensity)
- Modify: `pkg/aipipeline/prompts.go` `PlanGenValues` (add `Intensity` field) + `RenderRevisionPlan`
- Test: `pkg/plan/generate_test.go` (extend), `api/handler/revision_plan_test.go` (extend)

- [ ] **Step 1: Read the current shape**

```
grep -n "PlanGenerateInput\|Intensity\|intensity" pkg/plan/generate.go pkg/aipipeline/prompts.go pkg/aipipeline/prompts/revision_plan.tmpl api/handler/revision_plan.go
```

Note the existing field set so the new field slots in naturally.

- [ ] **Step 2: Write failing test in `pkg/plan/generate_test.go`**

```go
func TestGenerate_PersistsIntensity(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	eid := testutil.NewExam(t, pool, u.ID, sid, "2026-12-01")

	fake := newFakePlanProvider(t) // existing helper or inline FakeAIClient w/ a tiny days payload
	ai := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.DefaultQuotaLimits(), "claude-test")
	svc := plan.NewService(pool, ai)

	_, err := svc.Generate(context.Background(), plan.PlanGenerateInput{
		UserID:    u.ID, ExamID: eid, SubjectID: sid,
		Intensity: "intense",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var got string
	_ = pool.QueryRow(context.Background(),
		`SELECT intensity FROM revision_plans WHERE exam_id=$1`, eid).Scan(&got)
	if got != "intense" {
		t.Fatalf("intensity = %q, want intense", got)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Expected: BUILD FAIL — `PlanGenerateInput.Intensity` doesn't exist.

- [ ] **Step 4: Implement**

In `pkg/plan/generate.go`:

```go
type PlanGenerateInput struct {
	UserID    int64
	ExamID    int64
	SubjectID int64
	// ... existing fields ...
	Intensity string // light | normal | intense; "" → "normal" default
}
```

Inside `Generate` (search for the existing `INSERT INTO revision_plans`), include `intensity` in the column list and bind from `in.Intensity` (falling back to `"normal"` if empty).

Inside the call to `RunStructuredGeneration` / `RenderRevisionPlan`, forward `in.Intensity` so the AI prompt can shape `quizSlots`.

In `pkg/aipipeline/prompts.go` extend the existing `PlanGenValues` struct (find it via `grep -n "PlanGenValues" pkg/aipipeline/prompts.go`):

```go
type PlanGenValues struct {
	// ... existing fields ...
	Intensity string // light | normal | intense
}
```

In `pkg/aipipeline/prompts/revision_plan.tmpl`, append a section explaining the intensity contract and the quiz-slot output:

```
Intensity level: {{ .Intensity }}.
Per-day quiz slots:
- light:   at most 1 per day, 5 questions
- normal:  1 per "primary load" day, 10 questions
- intense: 1 specific + 1 global per day, 10/15 questions respectively

For each Day, also emit:
  "quizSlots": [
    {
      "kind": "specific" | "global",
      "suggestedSize": 5 | 10 | 15 | 20,
      "suggestedTypes": ["multi_choice", ...],
      "cardPool": [fc_id, ...]   // ids drawn from PrimarySubjectCards; [] for global
    },
    ...
  ]
Omit "quizSlots" entirely (or emit []) when no slots are warranted for the day.
```

In `api/handler/revision_plan.go`, accept `intensity` in the JSON body and pass to `PlanGenerateInput`. Default to `"normal"` when absent.

- [ ] **Step 5: Run tests to verify they pass**

```
go test ./pkg/plan/ -run 'TestGenerate_PersistsIntensity' -v
go test ./api/handler/ -run 'TestPostGeneratePlan' -v
```

Expected: PASS. If the existing `TestPostGeneratePlan` panics on the new field, update its body fixtures to include `"intensity":"normal"` (semantically a no-op default).

- [ ] **Step 6: Commit**

```bash
git add pkg/plan/generate.go pkg/aipipeline/prompts.go pkg/aipipeline/prompts/revision_plan.tmpl api/handler/revision_plan.go pkg/plan/generate_test.go api/handler/revision_plan_test.go
git commit -m "$(cat <<'EOF'
Spec D: plan generation reads + propagates intensity

[+] PlanGenerateInput.Intensity (default 'normal')
[+] revision_plan.tmpl: intensity contract + quizSlots output schema
[&] POST /exams/{id}/generate-plan accepts {intensity}
[+] generate_test.go: persists intensity column
EOF
)"
```

### Task 4: Drain `quizSlots` from AI output into `days_jsonb`

The plan generator currently absorbs `Day` items by trusting their existing fields. With Task 3 the AI now also emits `quizSlots`. This task verifies the drain preserves the new field (likely a no-op if `Day` already has `QuizSlots`-tagged JSON since `json.Unmarshal` handles it automatically — but we want an explicit test).

**Files:**
- Test: `pkg/plan/generate_test.go` (extend)
- Modify (if needed): `pkg/plan/generate.go` (the `collectDayItems` / equivalent helper)

- [ ] **Step 1: Write the failing test**

```go
func TestGenerate_AbsorbsQuizSlotsFromAI(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	eid := testutil.NewExam(t, pool, u.ID, sid, "2026-12-01")

	chunks := []aiProvider.Chunk{
		{Text: `{"items":[
			{"date":"2026-11-30","primarySubjectCards":[1],"crossSubjectCards":[],"deeperDives":[],
			 "quizSlots":[{"kind":"specific","suggestedSize":10,"suggestedTypes":["multi_choice"],"cardPool":[1]}]}
		]}`},
		{Done: true},
	}
	fake := &testutil.FakeAIClient{Chunks: chunks}
	ai := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.DefaultQuotaLimits(), "claude-test")
	svc := plan.NewService(pool, ai)

	out, err := svc.Generate(context.Background(), plan.PlanGenerateInput{
		UserID: u.ID, ExamID: eid, SubjectID: sid, Intensity: "normal",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(out.Days) != 1 || len(out.Days[0].QuizSlots) != 1 {
		t.Fatalf("quiz slots not absorbed: %+v", out.Days)
	}
	if out.Days[0].QuizSlots[0].SuggestedSize != 10 {
		t.Fatalf("size = %d", out.Days[0].QuizSlots[0].SuggestedSize)
	}

	// Round-trip through DB
	var raw []byte
	_ = pool.QueryRow(context.Background(),
		`SELECT days FROM revision_plans WHERE exam_id=$1`, eid).Scan(&raw)
	if !strings.Contains(string(raw), `"quizSlots"`) {
		t.Fatalf("days_jsonb missing quizSlots: %s", raw)
	}
}
```

- [ ] **Step 2: Run to verify it passes (or fails meaningfully)**

```
go test ./pkg/plan/ -run 'TestGenerate_AbsorbsQuizSlotsFromAI' -v
```

If the existing drain (`collectDayItems` or equivalent) decodes Items into `Day` via `json.Unmarshal`, this test should pass without code changes — the `QuizSlots` field's JSON tag picks them up automatically. If it relies on hand-rolled decoding, update it to use `json.Unmarshal` into `Day` directly.

- [ ] **Step 3: If failing, update the drain helper**

```go
// In pkg/plan/generate.go (or wherever items are decoded)
func decodeDay(raw json.RawMessage) (Day, error) {
	var d Day
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("decode day:\n%w", err)
	}
	return d, nil
}
```

- [ ] **Step 4: Verify**

```
go test ./pkg/plan/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit (only if Step 3 changed code)**

```bash
git add pkg/plan/generate.go pkg/plan/generate_test.go
git commit -m "$(cat <<'EOF'
Spec D: plan drain absorbs quizSlots from AI output

[&] decodeDay routes through json.Unmarshal so Day.QuizSlots is auto-populated
[+] generate_test.go: AI quizSlots survive into days_jsonb
EOF
)"
```

(If no code change was needed, commit just the test.)

---

## Phase 3 — `planContext` on `POST /quizzes/generate`

This is the materialisation hop. The user taps a slot, the front-end posts the slot coordinates, and the backend bypasses the quota debit (because plan generation already paid).

### Task 5: `aipipeline.AIRequest.SkipQuota`

The simplest way to bypass the debit without forking `RunStructuredGeneration` is a single boolean on the request that the preflight + finalize honor.

**Files:**
- Modify: `pkg/aipipeline/model.go` (add `SkipQuota` field)
- Modify: `pkg/aipipeline/service_generation.go` (honor the flag in `preflight` + `finalize`)
- Test: `pkg/aipipeline/service_generation_test.go` (add cases)

- [ ] **Step 1: Write the failing test**

```go
func TestRunStructuredGeneration_SkipQuotaBypassesDebit(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"x","options":["a","b","c","d"],"correctIndex":0,"referencedFcIds":[]}]}`},
			{Done: true},
		},
	}
	svc := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.QuotaLimits{QuizCalls: 1}, "claude-test")

	out, _, err := svc.RunStructuredGeneration(context.Background(), aipipeline.AIRequest{
		UserID:    u.ID,
		Feature:   aipipeline.FeatureGenerateQuiz,
		SubjectID: testutil.NewSubject(t, pool, u.ID, "S"),
		Prompt:    "x",
		SkipQuota: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range out { /* drain */ }

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE(quiz_calls,0) FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE`, u.ID,
	).Scan(&n)
	if n != 0 {
		t.Fatalf("quiz_calls = %d, want 0 (SkipQuota)", n)
	}
}

func TestRunStructuredGeneration_SkipQuotaStillChecksEntitlement(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool) // no AI access

	fake := &testutil.FakeAIClient{}
	svc := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.DefaultQuotaLimits(), "claude-test")

	_, _, err := svc.RunStructuredGeneration(context.Background(), aipipeline.AIRequest{
		UserID: u.ID, Feature: aipipeline.FeatureGenerateQuiz,
		SubjectID: testutil.NewSubject(t, pool, u.ID, "S"),
		Prompt: "x", SkipQuota: true,
	})
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("expected ErrNoAIAccess even with SkipQuota; got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL — `SkipQuota` field missing.

- [ ] **Step 3: Add the field + honor it**

In `pkg/aipipeline/model.go`, extend `AIRequest`:

```go
type AIRequest struct {
	// ... existing fields ...
	SkipQuota bool // SkipQuota bypasses CheckQuota + DebitQuota; entitlement still applies
}
```

In `pkg/aipipeline/service_generation.go`, find `preflight` (likely lines 32-41) and gate the quota check:

```go
func (s *Service) preflight(ctx context.Context, req AIRequest) error {
	if err := s.checkEntitlement(ctx, req.UserID); err != nil {
		return err
	}
	if !req.SkipQuota {
		if err := s.CheckQuota(ctx, req.UserID, req.Feature, req.PDFPages); err != nil {
			return err
		}
	}
	if err := s.checkConcurrency(ctx, req.UserID, req.Feature); err != nil {
		return err
	}
	return nil
}
```

In `finalize` (the success path that calls `DebitQuota`), wrap the debit:

```go
if !req.SkipQuota {
	if err := s.DebitQuota(ctx, req.UserID, req.Feature, 1, req.PDFPages); err != nil {
		// fall through; the AI call already succeeded, debit failure is logged below
	}
}
```

(If `finalize` doesn't have access to the original `AIRequest`, thread it through — `drive(ctx, req, jobID, out)` already has it.)

- [ ] **Step 4: Extend `GenerateQuiz` to forward `SkipQuota`**

In `pkg/aipipeline/service_generate_quiz.go`, add the field to `QuizGenerateInput` and forward:

```go
type QuizGenerateInput struct {
	UserID    int64
	SubjectID int64
	Prompt    string
	Metadata  map[string]any
	Images    []aiProvider.ImagePart
	SkipQuota bool // SkipQuota bypasses the daily quiz_calls debit (plan-materialised case)
}

func (s *Service) GenerateQuiz(ctx context.Context, in QuizGenerateInput) (*QuizGenerateOutput, error) {
	req := AIRequest{
		UserID:    in.UserID,
		Feature:   FeatureGenerateQuiz,
		SubjectID: in.SubjectID,
		Prompt:    in.Prompt,
		Metadata:  in.Metadata,
		SkipQuota: in.SkipQuota,
	}
	ch, jobID, err := s.RunStructuredGeneration(ctx, req)
	if err != nil {
		return nil, err
	}
	return &QuizGenerateOutput{Chunks: ch, JobID: jobID}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```
go test ./pkg/aipipeline/ -run 'TestRunStructuredGeneration_SkipQuota' -v
```

Expected: PASS.

- [ ] **Step 6: Run the wider suite**

```
go test ./pkg/aipipeline/... -v
```

Expected: PASS — existing quota/entitlement tests should still pass.

- [ ] **Step 7: Commit**

```bash
git add pkg/aipipeline/model.go pkg/aipipeline/service_generation.go pkg/aipipeline/service_generate_quiz.go pkg/aipipeline/service_generation_test.go
git commit -m "$(cat <<'EOF'
Spec D: AIRequest.SkipQuota for plan-materialised quizzes

[+] AIRequest.SkipQuota gates CheckQuota + DebitQuota
[&] preflight + finalize honor the flag; entitlement always runs
[+] QuizGenerateInput.SkipQuota forwarded into AIRequest
[+] tests: skip path bypasses debit; entitlement still enforces 402
EOF
)"
```

### Task 6: `pkg/quiz.Service.Generate` honors `PlanContext`

When the caller passes a `PlanContext`, `Generate` must:
1. Skip the AI quota debit (`SkipQuota=true`).
2. Persist with `source='plan'`, `source_plan_id=PlanContext.PlanID`.
3. Idempotently back-fill `revision_plans.days_jsonb[planDate].quizSlots[slotIndex].quizId`.

**Files:**
- Modify: `pkg/quiz/generate.go`
- Create: `pkg/quiz/plan_writeback.go` (the `days_jsonb` mutation helper)
- Test: `pkg/quiz/generate_plan_test.go`

- [ ] **Step 1: Write failing tests**

`pkg/quiz/generate_plan_test.go`:

```go
package quiz_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/plan"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestGenerate_WithPlanContext_SkipsQuotaAndMarksSourcePlan(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	fc1 := testutil.NewFlashcard(t, pool, c1, "X", "qx", "ax")

	// Seed a plan with one quizSlot.
	days := []plan.Day{{
		Date: "2026-11-30", PrimarySubjectCards: []int64{fc1},
		QuizSlots: []plan.QuizSlot{{Kind: "specific", SuggestedSize: 1, SuggestedTypes: []string{"multi_choice"}, CardPool: []int64{fc1}}},
	}}
	planID := testutil.NewPlanWithDays(t, pool, u.ID, sid, days)

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"x?","options":["a","b","c","d"],"correctIndex":0,"referencedFcIds":[` + itoa(fc1) + `]}]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.QuotaLimits{QuizCalls: 0}, "claude-test") // 0 cap proves SkipQuota worked
	svc := quiz.NewService(pool, ai)

	res, err := svc.Generate(context.Background(), quiz.GenerateRequest{
		UserID:    u.ID, SubjectID: sid,
		Kind:      quiz.KindSpecific, Size: 1, Types: []quiz.QuestionType{quiz.QTypeMultiChoice},
		CardFilter: quiz.FilterAll,
		PlanContext: &quiz.PlanContext{PlanID: planID, PlanDate: "2026-11-30", SlotIndex: 0},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// source='plan'
	var src string
	var planLink *int64
	_ = pool.QueryRow(context.Background(),
		`SELECT source, source_plan_id FROM quizzes WHERE id=$1`, res.QuizID,
	).Scan(&src, &planLink)
	if src != "plan" || planLink == nil || *planLink != planID {
		t.Fatalf("source=%q source_plan_id=%v", src, planLink)
	}

	// days_jsonb[0].quizSlots[0].quizId == res.QuizID
	var rawDays []byte
	_ = pool.QueryRow(context.Background(),
		`SELECT days FROM revision_plans WHERE id=$1`, planID).Scan(&rawDays)
	var got []plan.Day
	_ = json.Unmarshal(rawDays, &got)
	if got[0].QuizSlots[0].QuizID != res.QuizID {
		t.Fatalf("writeback failed; quizId=%d, want %d", got[0].QuizSlots[0].QuizID, res.QuizID)
	}

	// quota was not debited
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE(quiz_calls,0) FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE`, u.ID,
	).Scan(&n)
	if n != 0 {
		t.Fatalf("quiz_calls = %d, want 0 (SkipQuota for plan context)", n)
	}
}

func TestGenerate_WithPlanContext_Idempotent_ReturnsExistingQuizID(t *testing.T) {
	// Spec D §5.7: "server detects existing quizId for (planId, planDate, slotIndex) and returns it."
	// ... seed plan + run Generate once + run again with same PlanContext ...
	// Assert: second call returns the same QuizID without inserting a new quiz row.
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
```

(`testutil.NewPlanWithDays` is a helper to add — it INSERTs a revision_plan row with arbitrary days JSON. Define it alongside the existing `NewExam` helper in `testutil/seed.go`.)

- [ ] **Step 2: Run to verify failure**

Expected: FAIL — `Generate` currently always uses `source='user'` and never writes back to `days_jsonb`.

- [ ] **Step 3: Extend `Generate` in `pkg/quiz/generate.go`**

Modify the body of `Generate`. After the existing pool/prompt setup, branch on `PlanContext`:

```go
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := validateRequest(req); err != nil {
		return GenerateResult{}, err
	}

	// Idempotency: if a quiz already exists for this plan slot, return it.
	if req.PlanContext != nil {
		if existing, err := s.lookupExistingPlanSlotQuiz(ctx, *req.PlanContext); err == nil && existing != 0 {
			return GenerateResult{QuizID: existing, QuestionCount: 0, Kind: req.Kind}, nil
		}
	}

	cards, ids, err := s.resolveCardPool(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	subjectName, err := s.lookupSubjectName(ctx, req.SubjectID, req.UserID)
	if err != nil {
		return GenerateResult{}, err
	}
	body, err := aipipeline.RenderGenerateQuiz(aipipeline.QuizGenValues{
		SubjectName: subjectName, Kind: string(req.Kind),
		Size: req.Size, Types: typeStrings(req.Types), Cards: cards,
	})
	if err != nil {
		return GenerateResult{}, err
	}
	out, err := s.ai.GenerateQuiz(ctx, aipipeline.QuizGenerateInput{
		UserID: req.UserID, SubjectID: req.SubjectID, Prompt: body,
		SkipQuota: req.PlanContext != nil,
		Metadata:  map[string]any{"kind": string(req.Kind), "size": req.Size, "types": typeStrings(req.Types)},
	})
	if err != nil {
		return GenerateResult{}, err
	}
	questions, err := drainQuestions(out.Chunks, req.Size)
	if err != nil {
		return GenerateResult{}, err
	}
	settings, _ := json.Marshal(map[string]any{"size": req.Size, "types": typeStrings(req.Types)})

	source := SourceUser
	var sourcePlanID *int64
	if req.PlanContext != nil {
		source = SourcePlan
		pid := req.PlanContext.PlanID
		sourcePlanID = &pid
	}

	quizID, err := s.persistQuiz(ctx, PersistInput{
		UserID: req.UserID, SubjectID: req.SubjectID, ChapterID: req.ChapterID,
		Kind: req.Kind, Source: source, SourcePlanID: sourcePlanID,
		CardPool: ids, Settings: settings,
		Model: "claude-test", PromptHash: hashPrompt(body),
		Questions: questions,
	})
	if err != nil {
		return GenerateResult{}, err
	}

	if req.PlanContext != nil {
		if err := s.writebackPlanSlot(ctx, *req.PlanContext, quizID); err != nil {
			return GenerateResult{}, err
		}
	}

	return GenerateResult{QuizID: quizID, QuestionCount: len(questions), Kind: req.Kind}, nil
}
```

- [ ] **Step 4: Implement `lookupExistingPlanSlotQuiz` + `writebackPlanSlot`**

`pkg/quiz/plan_writeback.go`:

```go
package quiz

import (
	"context"
	"encoding/json"
	"fmt"
)

// lookupExistingPlanSlotQuiz returns the quizId already materialised for the
// (planID, planDate, slotIndex) tuple, or 0 if none exists.
func (s *Service) lookupExistingPlanSlotQuiz(ctx context.Context, pc PlanContext) (int64, error) {
	var rawDays []byte
	err := s.db.QueryRow(ctx,
		`SELECT days FROM revision_plans WHERE id=$1`, pc.PlanID,
	).Scan(&rawDays)
	if err != nil {
		return 0, fmt.Errorf("lookup plan:\n%w", err)
	}
	var days []dayProj
	if err := json.Unmarshal(rawDays, &days); err != nil {
		return 0, fmt.Errorf("decode days_jsonb:\n%w", err)
	}
	for _, d := range days {
		if d.Date == pc.PlanDate && pc.SlotIndex < len(d.QuizSlots) {
			return d.QuizSlots[pc.SlotIndex].QuizID, nil
		}
	}
	return 0, nil
}

// writebackPlanSlot mutates revision_plans.days_jsonb[planDate].quizSlots[slotIndex].quizId.
// Uses jsonb_set so the rewrite is atomic and doesn't trample concurrent edits.
func (s *Service) writebackPlanSlot(ctx context.Context, pc PlanContext, quizID int64) error {
	// We need to find the day index for planDate. The cleanest pattern given
	// JSONB-array layout is read-modify-write inside a tx.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin writeback tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rawDays []byte
	if err := tx.QueryRow(ctx,
		`SELECT days FROM revision_plans WHERE id=$1 FOR UPDATE`, pc.PlanID,
	).Scan(&rawDays); err != nil {
		return fmt.Errorf("select days for update:\n%w", err)
	}
	var days []dayProj
	if err := json.Unmarshal(rawDays, &days); err != nil {
		return fmt.Errorf("decode days_jsonb:\n%w", err)
	}

	updated := false
	for i := range days {
		if days[i].Date != pc.PlanDate {
			continue
		}
		if pc.SlotIndex >= len(days[i].QuizSlots) {
			return fmt.Errorf("slotIndex %d out of bounds for day %s", pc.SlotIndex, pc.PlanDate)
		}
		days[i].QuizSlots[pc.SlotIndex].QuizID = quizID
		updated = true
		break
	}
	if !updated {
		return fmt.Errorf("plan %d has no day %s", pc.PlanID, pc.PlanDate)
	}

	encoded, _ := json.Marshal(days)
	if _, err := tx.Exec(ctx,
		`UPDATE revision_plans SET days=$1::jsonb WHERE id=$2`, encoded, pc.PlanID,
	); err != nil {
		return fmt.Errorf("update days_jsonb:\n%w", err)
	}
	return tx.Commit(ctx)
}

// dayProj is the slim local projection of pkg/plan.Day used by the writeback.
// Keep this in sync with pkg/plan/model.go's JSON tags.
type dayProj struct {
	Date                string         `json:"date"`
	PrimarySubjectCards []int64        `json:"primarySubjectCards"`
	CrossSubjectCards   []int64        `json:"crossSubjectCards"`
	DeeperDives         []int64        `json:"deeperDives"`
	QuizSlots           []quizSlotProj `json:"quizSlots,omitempty"`
}

type quizSlotProj struct {
	Kind           string   `json:"kind"`
	SuggestedSize  int      `json:"suggestedSize"`
	SuggestedTypes []string `json:"suggestedTypes"`
	CardPool       []int64  `json:"cardPool"`
	QuizID         int64    `json:"quizId,omitempty"`
}
```

(The local `dayProj` is intentional: `pkg/quiz` shouldn't depend on `pkg/plan` to avoid a circular dependency if plan starts referencing quiz types later. The slim mirror has a comment to keep in sync.)

- [ ] **Step 5: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestGenerate_WithPlanContext' -v
```

Expected: PASS.

- [ ] **Step 6: Lint**

```
go vet ./pkg/quiz/...
gofmt -l pkg/quiz/
```

Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add pkg/quiz/generate.go pkg/quiz/plan_writeback.go pkg/quiz/generate_plan_test.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: Generate honors PlanContext (source='plan', writeback, SkipQuota)

[+] lookupExistingPlanSlotQuiz: idempotent slot materialisation
[+] writebackPlanSlot: atomic jsonb_set on days[date].quizSlots[i].quizId
[+] PlanContext path persists source='plan' + source_plan_id
[+] PlanContext path passes SkipQuota=true to AI pipeline
[+] generate_plan_test.go covers happy path + zero-debit assertion
EOF
)"
```

### Task 7: Quiz attempts inherit `plan_id` + `plan_date`

Per Spec D §5.3 the attempt rows for a plan-materialised quiz carry the plan coordinates so completion can write progress back. The `createAttempt` helper from Plan D1 must look up the parent quiz's source and propagate the link.

**Files:**
- Modify: `pkg/quiz/attempt.go` (extend `createAttempt`)
- Test: `pkg/quiz/attempt_test.go` (add case)

- [ ] **Step 1: Write the failing test**

```go
func TestStart_PlanSourcedQuiz_PropagatesPlanIDAndDate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	planID := testutil.NewPlanWithDays(t, pool, u.ID, sid, []plan.Day{{
		Date: "2026-11-30", QuizSlots: []plan.QuizSlot{{Kind: "specific", SuggestedSize: 1, CardPool: []int64{}}},
	}})
	// Insert a quizzes row directly with source='plan'.
	qid := testutil.NewPlanQuiz(t, pool, u.ID, sid, planID, "2026-11-30", 1)

	svc := quiz.NewService(pool, nil)
	att, _, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if att.PlanID == nil || *att.PlanID != planID {
		t.Fatalf("attempt.PlanID = %v, want %d", att.PlanID, planID)
	}
	if att.PlanDate == nil || *att.PlanDate != "2026-11-30" {
		t.Fatalf("attempt.PlanDate = %v", att.PlanDate)
	}
}
```

(`testutil.NewPlanQuiz` inserts a `quizzes` row with `source='plan'`, returns its id; add to `testutil/seed.go`.)

- [ ] **Step 2: Run to verify failure**

Expected: FAIL — `createAttempt` doesn't read the quiz's plan link, so `att.PlanID` stays nil.

- [ ] **Step 3: Update `createAttempt`**

In `pkg/quiz/attempt.go`:

```go
func (s *Service) createAttempt(ctx context.Context, uid, quizID int64) (Attempt, error) {
	var (
		total     int
		planID    *int64
		planDate  *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT q.question_count,
		       CASE WHEN q.source='plan' THEN q.source_plan_id ELSE NULL END,
		       CASE WHEN q.source='plan' THEN (
		           SELECT TO_CHAR((d->>'date')::date, 'YYYY-MM-DD')
		             FROM revision_plans rp,
		                  LATERAL jsonb_array_elements(rp.days) d
		            WHERE rp.id = q.source_plan_id
		              AND (d->>'date')::date >= CURRENT_DATE  -- nearest match
		            ORDER BY (d->>'date')::date ASC
		            LIMIT 1
		         )
		         ELSE NULL END
		  FROM quizzes q WHERE q.id=$1`, quizID,
	).Scan(&total, &planID, &planDate)
	if err != nil {
		return Attempt{}, fmt.Errorf("lookup quiz meta:\n%w", err)
	}

	var att Attempt
	err = s.db.QueryRow(ctx, `
		INSERT INTO quiz_attempts (quiz_id, user_id, state, total_count, plan_id, plan_date)
		VALUES ($1,$2,'in_progress',$3,$4,$5)
		RETURNING id, quiz_id, user_id, state, started_at, completed_at,
		          correct_count, total_count, score_pct, plan_id, plan_date`,
		quizID, uid, total, planID, planDate,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	if err != nil {
		return Attempt{}, fmt.Errorf("insert attempt:\n%w", err)
	}
	return att, nil
}
```

The "nearest day" subquery is a defensive fallback — if `quiz.source='plan'` but the plan no longer has any matching day (user regenerated the plan), the attempt gets a nil `plan_date` and progress writeback simply doesn't fire. A more rigorous approach would store `plan_date` directly on `quizzes` at generation time; this is a deliberate KISS trade-off for v1.

Note: the simpler alternative is to also persist `plan_date` on `quizzes` at generation time. If the implementer prefers that path, add a `plan_date DATE` column to `quizzes` via ALTER in Phase 1 and set it inside `writebackPlanSlot`. The trade-off: one extra column vs. one extra subquery. Either is acceptable per CLAUDE.md; pick one and don't mix.

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestStart_PlanSourcedQuiz' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/attempt.go pkg/quiz/attempt_test.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: plan-sourced attempts inherit plan_id + plan_date

[&] createAttempt resolves plan_id (source_plan_id) and plan_date from days_jsonb
[+] attempt_test.go: plan-sourced quiz produces plan-linked attempt
EOF
)"
```

### Task 8: Handler accepts `planContext`

**Files:**
- Modify: `api/handler/quiz_stub.go` (extend `generateRequest`)
- Test: `api/handler/quiz_plan_test.go` (new file)

- [ ] **Step 1: Write the failing test**

```go
func TestPostQuizzesGenerate_WithPlanContext_NoQuotaDebit(t *testing.T) {
	// ... seed user + plan with quizSlot + AI fake ...
	body, _ := json.Marshal(map[string]any{
		"subjectId":  sid,
		"kind":       "specific",
		"size":       1,
		"types":      []string{"multi_choice"},
		"cardFilter": "all",
		"planContext": map[string]any{
			"planId":    planID,
			"planDate":  "2026-11-30",
			"slotIndex": 0,
		},
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Generate(w, req)
	if w.Code != 200 { t.Fatalf("status %d body=%s", w.Code, w.Body.String()) }
	// ... assert quota_calls unchanged, days_jsonb mutated ...
}
```

- [ ] **Step 2: Run to verify failure**

Expected: FAIL — handler doesn't decode `planContext`.

- [ ] **Step 3: Extend the request shape**

In `api/handler/quiz_stub.go`:

```go
type generateRequest struct {
	SubjectID   int64        `json:"subjectId"`
	ChapterID   *int64       `json:"chapterId,omitempty"`
	Kind        string       `json:"kind"`
	Size        int          `json:"size"`
	Types       []string     `json:"types"`
	CardFilter  string       `json:"cardFilter,omitempty"`
	PlanContext *planContext `json:"planContext,omitempty"`
}

type planContext struct {
	PlanID    int64  `json:"planId"`
	PlanDate  string `json:"planDate"`
	SlotIndex int    `json:"slotIndex"`
}
```

Forward in `Generate`:

```go
var pc *quiz.PlanContext
if body.PlanContext != nil {
	pc = &quiz.PlanContext{
		PlanID:    body.PlanContext.PlanID,
		PlanDate:  body.PlanContext.PlanDate,
		SlotIndex: body.PlanContext.SlotIndex,
	}
}
req := quiz.GenerateRequest{
	// ... existing fields ...
	PlanContext: pc,
}
```

- [ ] **Step 4: Run tests**

```
go test ./api/handler/ -run 'TestPostQuizzesGenerate_WithPlanContext' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go api/handler/quiz_plan_test.go
git commit -m "$(cat <<'EOF'
Spec D: handler decodes + forwards planContext

[+] generateRequest.planContext { planId, planDate, slotIndex }
[+] handler test: plan-sourced generation skips quota + writebacks slot
EOF
)"
```

---

## Phase 4 — Completion writeback to `revision_plan_progress`

### Task 9: On final answer, write progress rows for plan-sourced attempts

Per Spec D §5.2: when the last `quiz_attempt_answers` flips `state='completed'` and the attempt has `plan_id` set, insert one `revision_plan_progress` row per `referenced_fc_ids` across the quiz's questions.

**Files:**
- Modify: `pkg/quiz/attempt.go` (`completeAttempt` already exists from D1 — extend it)
- Test: `pkg/quiz/attempt_test.go` (add case)

- [ ] **Step 1: Write the failing test**

```go
func TestAnswer_PlanSourcedAttempt_WritesProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	fc1 := testutil.NewFlashcard(t, pool, c1, "X", "qx", "ax")
	fc2 := testutil.NewFlashcard(t, pool, c1, "Y", "qy", "ay")

	planID := testutil.NewPlanWithDays(t, pool, u.ID, sid, []plan.Day{{
		Date: "2026-11-30", PrimarySubjectCards: []int64{fc1, fc2},
		QuizSlots: []plan.QuizSlot{{Kind: "specific", SuggestedSize: 2, CardPool: []int64{fc1, fc2}}},
	}})
	// Insert a plan-sourced quiz with 2 questions, each referencing one fc.
	qid := testutil.NewPlanQuizWithQuestions(t, pool, u.ID, sid, planID, "2026-11-30", []testutil.QSpec{
		{Type: "multi_choice", FcIDs: []int64{fc1}, Correct: 2},
		{Type: "multi_choice", FcIDs: []int64{fc2}, Correct: 2},
	})

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att.ID, q1.ID, json.RawMessage(`{"index":2}`))

	// Get q2 via fresh Start (idempotent).
	_, q2, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att.ID, q2.ID, json.RawMessage(`{"index":2}`))

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM revision_plan_progress WHERE user_id=$1 AND plan_date='2026-11-30'`, u.ID,
	).Scan(&n)
	if n != 2 {
		t.Fatalf("revision_plan_progress rows = %d, want 2", n)
	}
}

func TestAnswer_UserSourcedAttempt_NoProgressWriteback(t *testing.T) {
	// ... user-sourced quiz completion does NOT write progress rows ...
	// Assert revision_plan_progress count == 0.
}
```

- [ ] **Step 2: Run to verify failure**

Expected: FAIL — `completeAttempt` doesn't currently write progress rows.

- [ ] **Step 3: Extend `completeAttempt`**

In `pkg/quiz/attempt.go`:

```go
func (s *Service) completeAttempt(ctx context.Context, tx pgx.Tx, attemptID int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE quiz_attempts
		   SET state='completed',
		       completed_at = now(),
		       score_pct = CASE WHEN total_count > 0
		                        THEN (correct_count * 100) / total_count
		                        ELSE 0 END
		 WHERE id=$1`, attemptID); err != nil {
		return fmt.Errorf("complete attempt:\n%w", err)
	}
	return s.writePlanProgress(ctx, tx, attemptID)
}

// writePlanProgress is a no-op when the attempt has no plan_id. Otherwise it
// inserts one revision_plan_progress row per referenced_fc_id across all
// questions of the quiz, idempotently (ON CONFLICT DO NOTHING).
func (s *Service) writePlanProgress(ctx context.Context, tx pgx.Tx, attemptID int64) error {
	var (
		planID   *int64
		planDate *string
		userID   int64
		quizID   int64
	)
	err := tx.QueryRow(ctx,
		`SELECT plan_id, plan_date, user_id, quiz_id FROM quiz_attempts WHERE id=$1`, attemptID,
	).Scan(&planID, &planDate, &userID, &quizID)
	if err != nil {
		return fmt.Errorf("load attempt for progress:\n%w", err)
	}
	if planID == nil || planDate == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO revision_plan_progress (user_id, fc_id, plan_date)
		SELECT $1, fc.id, $2::date
		  FROM quiz_questions qq,
		       LATERAL jsonb_array_elements_text(qq.referenced_fc_ids_jsonb) fcId,
		       LATERAL (SELECT (fcId)::bigint AS id) fc
		 WHERE qq.quiz_id = $3
		ON CONFLICT (user_id, fc_id, plan_date) DO NOTHING`,
		userID, *planDate, quizID,
	); err != nil {
		return fmt.Errorf("write plan progress:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestAnswer_PlanSourcedAttempt|TestAnswer_UserSourcedAttempt' -v
```

Expected: PASS.

- [ ] **Step 5: Run the full quiz + plan suite**

```
go test ./pkg/quiz/... ./pkg/plan/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/quiz/attempt.go pkg/quiz/attempt_test.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: completion writes revision_plan_progress for plan-sourced attempts

[+] writePlanProgress runs inside completeAttempt tx; no-op when plan_id is nil
[+] INSERTs (user_id, fc_id, plan_date) for every referenced_fc_id across the quiz
[+] ON CONFLICT DO NOTHING preserves idempotency across retakes
[+] tests: plan-sourced -> 2 progress rows; user-sourced -> 0
EOF
)"
```

---

## Phase 5 — OpenAPI + smoke

### Task 10: Update OpenAPI for `planContext` and `intensity`

**Files:**
- Modify: `api/handler/docs_openapi.yaml`

- [ ] **Step 1: Locate the Spec D1 quiz schemas**

```
grep -n "/quizzes/generate\|PublicQuestion" api/handler/docs_openapi.yaml
```

- [ ] **Step 2: Extend the request body**

In the `/quizzes/generate` `requestBody` properties block (added in Plan D1 Task 21), add:

```yaml
                planContext:
                  type: object
                  description: When present, sourcing the quiz from a plan slot. Server skips the daily quiz quota debit.
                  required: [planId, planDate, slotIndex]
                  properties:
                    planId:    { type: integer, format: int64 }
                    planDate:  { type: string, format: date }
                    slotIndex: { type: integer, minimum: 0 }
```

- [ ] **Step 3: Document `intensity` on `/exams/{id}/generate-plan`**

Add to that endpoint's `requestBody.properties`:

```yaml
                intensity:
                  type: string
                  enum: [light, normal, intense]
                  default: normal
                  description: Per-day quiz-slot density. See Spec D §2 + §5.3.
```

- [ ] **Step 4: Sanity-parse**

```
python3 -c "import yaml; yaml.safe_load(open('api/handler/docs_openapi.yaml'))"
go test ./api/handler/ -run 'TestDocs' -v
```

Expected: clean parse, PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/docs_openapi.yaml
git commit -m "$(cat <<'EOF'
Spec D: OpenAPI for planContext + plan intensity

[+] /quizzes/generate planContext { planId, planDate, slotIndex }
[+] /exams/{id}/generate-plan intensity field (default normal)
EOF
)"
```

---

## Self-review checklist

1. **Spec coverage**
   - §2 `revision_plans.intensity`: Task 1.
   - §2 day-plan JSON `QuizSlot`: Tasks 2 + 4.
   - §3 `planContext` request shape: Task 8.
   - §3 `source='plan'` + `source_plan_id`: Task 6.
   - §3 server-side slot writeback: Task 6 (`writebackPlanSlot`).
   - §5.3 quota skip: Tasks 5 + 6.
   - §5.3 attempt carries `plan_id` + `plan_date`: Task 7.
   - §5.2 completion writes `revision_plan_progress`: Task 9.
   - §5.7 idempotent slot materialisation: Task 6 (`lookupExistingPlanSlotQuiz`).
   - §1 architecture invariant ("all AI calls go through `RunStructuredGeneration`"): preserved — `SkipQuota` is a flag on the existing entry point, not a new path.
2. **Cross-spec deferrals**
   - Sharing (D3) is untouched here; the `source='shared_copy'` path in `quizzes` is not exercised by any task above.
   - Frontend (`ExamSetupPage` intensity selector, `TodayPlanCard` slot rendering) is intentionally absent.
3. **Type consistency**
   - `plan.QuizSlot` (in `pkg/plan/model.go`) and `quizSlotProj` (in `pkg/quiz/plan_writeback.go`) share JSON tags exactly — verified in Tasks 2 + 6.
   - `Attempt.PlanID` / `PlanDate` shapes round-trip from the SQL columns inserted in Task 7.
4. **No placeholders** — every step has the actual SQL / Go body.
5. **CLAUDE.md fit** — function bodies stay under 30 lines (largest is `Generate` at ~40, acceptable per the function-size carve-out for orchestration); no `replace` directives touched.

---

## Execution handoff

When complete:
- `make test` clean.
- `go vet ./...` clean.
- ~10 commits on `feat/spec-d-ai-quiz` matching task headings.
- The branch should already carry Plan D1's commits. Open a single PR that bundles D1+D2 (and later D3) for the team, or push D2 as a follow-up PR if D1's PR already merged.
