# Spec B Fidelity Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining gaps between Spec B (`docs/superpowers/specs/2026-04-19-ai-revision-plan-design.md`) and the implementation: replace the destructive boot-time schema rewrite with idempotent statements, add the partial active-exams index, persist the AI job id on each plan, and amend the spec where the code intentionally diverged.

**Architecture:** Three pure-code tasks plus one spec-amendment task. Code changes are confined to `db_sql/setup_plan.go`, `pkg/plan/{model,persist,generate}.go`, and `pkg/plan/read.go`. The schema migration is additive and idempotent (no destructive `DROP TABLE`); existing `exams` / `revision_plans` / `revision_plan_progress` rows survive a redeploy.

**Tech Stack:** Go 1.x, pgx/v5, Postgres. Tests use `testutil.OpenTestDB` + `testutil.Reset` (truncate-and-reseed against `studbud_test`).

---

## Pre-flight: confirm current state

Before starting, confirm the working tree matches the assumptions in this plan.

- [ ] **Step 0.1: Confirm baseline files**

```bash
grep -n "DROP TABLE IF EXISTS" /Users/martonroux/Documents/WEB/studbud_3/backend/db_sql/setup_plan.go
grep -n "idx_exams_user_active" /Users/martonroux/Documents/WEB/studbud_3/backend/db_sql/setup_plan.go
grep -n "generation_id" /Users/martonroux/Documents/WEB/studbud_3/backend/db_sql/setup_plan.go
```

Expected:
- The `DROP TABLE` lines are present (line ~14-16).
- `idx_exams_user_active` exists without a `WHERE` clause (line ~29).
- No `generation_id` references yet.

If any of these don't match, stop and re-read the audit before proceeding.

- [ ] **Step 0.2: Run the existing test suite to lock in a green baseline**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./pkg/plan/... ./pkg/aipipeline/...
```

Expected: PASS. If any test is already red, fix that before starting.

---

## Task 1: Idempotent, partial-index plan schema

Replace the destructive `DROP TABLE … CASCADE` preamble with idempotent `CREATE TABLE IF NOT EXISTS` statements, recreate `idx_exams_user_active` as a partial index per Spec §4.1, and add `revision_plans.generation_id` per Spec §4.2.

**Files:**
- Modify: `db_sql/setup_plan.go` (rewrite the `planSchema` constant; keep `setupPlan` body unchanged)
- Test: `db_sql/setup_plan_test.go` (new)

- [ ] **Step 1.1: Write the failing schema-survival test**

Create `/Users/martonroux/Documents/WEB/studbud_3/backend/db_sql/setup_plan_test.go`:

```go
package db_sql

import (
	"context"
	"testing"
	"time"

	"studbud/backend/testutil"
)

// TestSetupPlan_PreservesExistingRows guards the idempotency contract: running
// SetupAll twice must not destroy data written between the two calls.
func TestSetupPlan_PreservesExistingRows(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	var userID, subjectID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO users (email, password_hash, role)
        VALUES ('plan-survive@example.com', 'x', 'user') RETURNING id
    `).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO subjects (owner_id, name) VALUES ($1, 'Bio') RETURNING id
    `, userID).Scan(&subjectID); err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	var examID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, date, title)
        VALUES ($1, $2, $3, 'Partiel') RETURNING id
    `, userID, subjectID, time.Now().AddDate(0, 0, 14)).Scan(&examID); err != nil {
		t.Fatalf("seed exam: %v", err)
	}

	if err := setupPlan(ctx, pool); err != nil {
		t.Fatalf("re-run setupPlan: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exams WHERE id = $1`, examID).Scan(&n); err != nil {
		t.Fatalf("count exams: %v", err)
	}
	if n != 1 {
		t.Fatalf("exam survived re-setup: count = %d, want 1", n)
	}
}

// TestSetupPlan_PartialIndexHasPredicate asserts the active-exams index uses
// the WHERE date >= CURRENT_DATE predicate (Spec B §4.1).
func TestSetupPlan_PartialIndexHasPredicate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	ctx := context.Background()

	var indexDef string
	err := pool.QueryRow(ctx, `
        SELECT indexdef FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'idx_exams_user_active'
    `).Scan(&indexDef)
	if err != nil {
		t.Fatalf("read index def: %v", err)
	}
	if !contains(indexDef, "WHERE") || !contains(indexDef, "CURRENT_DATE") {
		t.Fatalf("idx_exams_user_active is not partial: %q", indexDef)
	}
}

// TestSetupPlan_RevisionPlansHasGenerationID asserts the generation_id column
// is present on revision_plans (Spec B §4.2).
func TestSetupPlan_RevisionPlansHasGenerationID(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	ctx := context.Background()

	var n int
	err := pool.QueryRow(ctx, `
        SELECT count(*) FROM information_schema.columns
        WHERE table_name = 'revision_plans' AND column_name = 'generation_id'
    `).Scan(&n)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if n != 1 {
		t.Fatalf("revision_plans.generation_id missing: count = %d", n)
	}
}

// contains is a tiny substring helper to keep the asserts terse.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 1.2: Run the new test and confirm it fails**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./db_sql/... -run TestSetupPlan -v
```

Expected: `TestSetupPlan_PreservesExistingRows` and `TestSetupPlan_RevisionPlansHasGenerationID` fail (existing rows are wiped by the `DROP TABLE` preamble; `generation_id` does not exist). `TestSetupPlan_PartialIndexHasPredicate` fails because the predicate is missing.

- [ ] **Step 1.3: Rewrite `db_sql/setup_plan.go`**

Replace the entire `planSchema` constant and surrounding comment. The new file is:

```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// planSchema is idempotent: safe to run on every boot and on every test setup.
// Schema shape follows Spec B §4 (exams, revision_plans, revision_plan_progress).
const planSchema = `
CREATE TABLE IF NOT EXISTS exams (
    id                BIGSERIAL    PRIMARY KEY,
    user_id           BIGINT       NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    subject_id        BIGINT       NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    date              DATE         NOT NULL,
    title             TEXT         NOT NULL,
    notes             TEXT,
    annales_image_id  TEXT         REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
DROP INDEX IF EXISTS idx_exams_user_active;
CREATE INDEX idx_exams_user_active ON exams (user_id, date) WHERE date >= CURRENT_DATE;

CREATE TABLE IF NOT EXISTS revision_plans (
    id            BIGSERIAL    PRIMARY KEY,
    exam_id       BIGINT       NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    generated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    days          JSONB        NOT NULL,
    model         TEXT         NOT NULL,
    prompt_hash   TEXT         NOT NULL
);
ALTER TABLE revision_plans
    ADD COLUMN IF NOT EXISTS generation_id BIGINT NULL REFERENCES ai_jobs(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS revision_plan_progress (
    user_id   BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    fc_id     BIGINT      NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    plan_date DATE        NOT NULL,
    done_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, fc_id, plan_date)
);
CREATE INDEX IF NOT EXISTS idx_rpp_user_today ON revision_plan_progress (user_id, plan_date);
`

// setupPlan installs the Spec B revision-plan schema. Idempotent.
func setupPlan(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, planSchema); err != nil {
		return fmt.Errorf("exec plan schema:\n%w", err)
	}
	return nil
}
```

Note: `ai_jobs` is created by `setupAI`, which runs before `setupPlan` per `db_sql/setup.go:14-23` — the `REFERENCES ai_jobs(id)` clause is safe.

- [ ] **Step 1.4: Manually drop and recreate the test database to clear stale state**

The `studbud_test` DB likely has a non-partial `idx_exams_user_active` index from prior runs. The new `DROP INDEX IF EXISTS` line in `planSchema` handles this on next boot, but for a clean run:

```bash
psql postgres://localhost/postgres -c "DROP DATABASE IF EXISTS studbud_test"
psql postgres://localhost/postgres -c "CREATE DATABASE studbud_test"
```

- [ ] **Step 1.5: Run the schema tests and confirm they pass**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./db_sql/... -run TestSetupPlan -v
```

Expected: all three subtests PASS.

- [ ] **Step 1.6: Run the full backend test suite**

```bash
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./...
```

Expected: PASS. If `pkg/plan` or `pkg/aipipeline` tests fail, investigate before committing — the model and persist code still references the old shape.

- [ ] **Step 1.7: Commit**

```bash
git add db_sql/setup_plan.go db_sql/setup_plan_test.go
git commit -m "$(cat <<'EOF'
Spec B: idempotent plan schema + partial active-exams index

[&] setup_plan.go uses CREATE TABLE IF NOT EXISTS (no more boot-time data wipe)
[+] idx_exams_user_active is now partial (WHERE date >= CURRENT_DATE)
[+] revision_plans.generation_id BIGINT REFERENCES ai_jobs(id)
[+] setup_plan_test.go: row-survival, partial-index, generation_id checks
EOF
)"
```

---

## Task 2: Persist `generation_id` on each plan

Thread the `ai_jobs.id` returned by `aipipeline.GenerateRevisionPlan` into the new `revision_plans.generation_id` column. Surface it on the read path so future debugging tools can correlate a plan with its AI job audit row.

**Files:**
- Modify: `pkg/plan/model.go` (add `GenerationID *int64` to `Plan`)
- Modify: `pkg/plan/persist.go` (extend `persist` signature, INSERT, SELECT)
- Modify: `pkg/plan/generate.go` (return `JobID` from `streamPlanGeneration`; pass through `runPlanningPhase` → `persist`)

- [ ] **Step 2.1: Add `GenerationID` to the Plan model**

Edit `/Users/martonroux/Documents/WEB/studbud_3/backend/pkg/plan/model.go`. After the `GeneratedAt` field of `Plan`:

```go
// Plan is the persisted output of a generation run.
type Plan struct {
	ID           int64     `json:"id"`                     // ID is the BIGSERIAL primary key
	ExamID       int64     `json:"examId"`                 // ExamID is the owning exam
	Days         []Day     `json:"days"`                   // Days is the per-day schedule from today → exam date
	Model        string    `json:"model"`                  // Model is the AI model identifier used to generate the plan
	PromptHash   string    `json:"promptHash"`             // PromptHash captures the prompt revision for debugging plan drift
	GeneratedAt  time.Time `json:"generatedAt"`            // GeneratedAt is the persistence timestamp
	GenerationID *int64    `json:"generationId,omitempty"` // GenerationID is the ai_jobs row id that produced this plan (nil for legacy rows)
}
```

- [ ] **Step 2.2: Extend `persist` to write `generation_id` and update `loadPlanByExam` to read it**

Edit `/Users/martonroux/Documents/WEB/studbud_3/backend/pkg/plan/persist.go`. Replace the `persist` and `loadPlanByExam` functions:

```go
// persist replaces any existing plan for examID with a freshly generated one.
// Runs DELETE + INSERT in a single transaction so a failed insert leaves
// the previous plan intact. generationID, when non-nil, links the plan to
// the originating ai_jobs row.
func (s *Service) persist(ctx context.Context, examID int64, days []Day, model, promptHash string, generationID *int64) (*Plan, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM revision_plans WHERE exam_id = $1`, examID); err != nil {
		return nil, fmt.Errorf("delete prior plan:\n%w", err)
	}
	daysJSON, err := json.Marshal(days)
	if err != nil {
		return nil, fmt.Errorf("marshal days:\n%w", err)
	}
	plan := Plan{ExamID: examID, Days: days, Model: model, PromptHash: promptHash, GenerationID: generationID}
	row := tx.QueryRow(ctx, `
        INSERT INTO revision_plans (exam_id, days, model, prompt_hash, generation_id)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, generated_at
    `, examID, daysJSON, model, promptHash, generationID)
	if err := row.Scan(&plan.ID, &plan.GeneratedAt); err != nil {
		return nil, fmt.Errorf("insert plan:\n%w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit plan:\n%w", err)
	}
	return &plan, nil
}

// loadPlanByExam returns the stored plan for examID, or pgx.ErrNoRows if none exists.
func (s *Service) loadPlanByExam(ctx context.Context, examID int64) (*Plan, error) {
	var p Plan
	var daysRaw []byte
	err := s.db.QueryRow(ctx, `
        SELECT id, exam_id, days, model, prompt_hash, generated_at, generation_id
        FROM revision_plans WHERE exam_id = $1
    `, examID).Scan(&p.ID, &p.ExamID, &daysRaw, &p.Model, &p.PromptHash, &p.GeneratedAt, &p.GenerationID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(daysRaw, &p.Days); err != nil {
		return nil, fmt.Errorf("unmarshal days:\n%w", err)
	}
	return &p, nil
}
```

- [ ] **Step 2.3: Thread `JobID` through the orchestrator**

Edit `/Users/martonroux/Documents/WEB/studbud_3/backend/pkg/plan/generate.go`. Replace the `streamPlanGeneration` and `runPlanningPhase` functions:

```go
// runPlanningPhase renders + invokes the plan-generation prompt and persists.
// Emits PhasePlanning. Returns the persisted plan on success.
func (s *Service) runPlanningPhase(
	ctx context.Context, userID int64, exm *exam.Exam,
	primary []PrimaryCard, candidates []Candidate, images []aiProvider.ImagePart,
	out chan<- Event,
) (*Plan, error) {
	emit(out, Event{Phase: PhasePlanning})
	stats, err := s.loadStateCounts(ctx, exm.SubjectID)
	if err != nil {
		return nil, err
	}
	prompt, err := s.renderPlanPrompt(ctx, exm, primary, candidates, stats, len(images) > 0)
	if err != nil {
		return nil, err
	}
	days, jobID, err := s.streamPlanGeneration(ctx, userID, exm, prompt, images)
	if err != nil {
		return nil, err
	}
	cleaned := normalizePlan(days, primary, candidates, time.Now(), exm.ExamDate)
	return s.persist(ctx, exm.ID, cleaned, s.model, hashPrompt(prompt), &jobID)
}

// streamPlanGeneration calls aipipeline.GenerateRevisionPlan, drains its chunk
// stream, and reassembles the AI's per-day items into a []Day. The returned
// jobID identifies the ai_jobs row for audit-correlation.
// Quota debit + ai_jobs accounting are owned by RunStructuredGeneration.
func (s *Service) streamPlanGeneration(ctx context.Context, userID int64, exm *exam.Exam, prompt string, images []aiProvider.ImagePart) ([]Day, int64, error) {
	out, err := s.ai.GenerateRevisionPlan(ctx, aipipeline.PlanGenerateInput{
		UserID:        userID,
		ExamID:        exm.ID,
		SubjectID:     exm.SubjectID,
		Prompt:        prompt,
		AnnalesImages: images,
	})
	if err != nil {
		return nil, 0, err
	}
	days, err := collectDayItems(ctx, out.Chunks)
	if err != nil {
		return nil, out.JobID, err
	}
	return days, out.JobID, nil
}
```

- [ ] **Step 2.4: Run `go build` to confirm the persist signature change is consistent**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
go build ./...
```

Expected: clean build. If any other caller of `persist` shows up, update it to pass `nil` as the new last argument and report back — Spec B as written has only one caller (`runPlanningPhase`).

- [ ] **Step 2.5: Add an integration test that asserts `generation_id` is populated**

Edit `/Users/martonroux/Documents/WEB/studbud_3/backend/pkg/aipipeline/service_revision_test.go` — find the existing `TestGenerateRevisionPlan*` happy-path test that already drives an end-to-end generation, and add an assertion at the end. If that test doesn't exist with that exact shape, add a new one in `pkg/plan/persist_test.go`:

Create `/Users/martonroux/Documents/WEB/studbud_3/backend/pkg/plan/persist_test.go`:

```go
package plan

import (
	"context"
	"testing"
	"time"

	"studbud/backend/testutil"
)

// TestPersist_WritesGenerationID confirms the generation_id column is populated
// when persist receives a non-nil jobID, and that loadPlanByExam round-trips it.
func TestPersist_WritesGenerationID(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	var userID, subjectID, examID, jobID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO users (email, password_hash, role)
        VALUES ('persist-gen-id@example.com', 'x', 'user') RETURNING id
    `).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO subjects (owner_id, name) VALUES ($1, 'Bio') RETURNING id
    `, userID).Scan(&subjectID); err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, date, title)
        VALUES ($1, $2, $3, 'Partiel') RETURNING id
    `, userID, subjectID, time.Now().AddDate(0, 0, 14)).Scan(&examID); err != nil {
		t.Fatalf("seed exam: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO ai_jobs (user_id, feature_key, model, status)
        VALUES ($1, 'revision_plan', 'test-model', 'complete') RETURNING id
    `, userID).Scan(&jobID); err != nil {
		t.Fatalf("seed ai_job: %v", err)
	}

	s := &Service{db: pool, model: "test-model"}
	days := []Day{{Date: "2026-05-09", PrimarySubjectCards: []int64{}}}
	plan, err := s.persist(ctx, examID, days, "test-model", "deadbeef", &jobID)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if plan.GenerationID == nil || *plan.GenerationID != jobID {
		t.Fatalf("plan.GenerationID = %v, want %d", plan.GenerationID, jobID)
	}

	loaded, err := s.loadPlanByExam(ctx, examID)
	if err != nil {
		t.Fatalf("loadPlanByExam: %v", err)
	}
	if loaded.GenerationID == nil || *loaded.GenerationID != jobID {
		t.Fatalf("loaded.GenerationID = %v, want %d", loaded.GenerationID, jobID)
	}
}
```

- [ ] **Step 2.6: Run the new test and confirm it passes**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./pkg/plan/... -run TestPersist_WritesGenerationID -v
```

Expected: PASS.

- [ ] **Step 2.7: Run the full plan + aipipeline suites**

```bash
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./pkg/plan/... ./pkg/aipipeline/... -v
```

Expected: PASS across the board, including the existing `TestGenerateRevisionPlan*` integration tests (the new field is optional / nullable, so they keep working without modification).

- [ ] **Step 2.8: Commit**

```bash
git add pkg/plan/model.go pkg/plan/persist.go pkg/plan/generate.go pkg/plan/persist_test.go
git commit -m "$(cat <<'EOF'
Spec B: persist ai_jobs id on revision_plans.generation_id

[+] Plan.GenerationID *int64 (json:generationId,omitempty)
[&] persist signature takes generationID; INSERT writes the column
[&] loadPlanByExam SELECTs and populates GenerationID
[&] streamPlanGeneration returns (days, jobID, error); runPlanningPhase threads jobID into persist
[+] persist_test.go: TestPersist_WritesGenerationID round-trip
EOF
)"
```

---

## Task 3: Verify the quota debit pattern (no code expected)

Confirm that the implementation's debit-on-success pattern (`pkg/aipipeline/service_generation.go:218`) is functionally equivalent to Spec §6.1's "debit + refund on failure" wording, then document the conclusion. No code change is expected; if the trace surfaces a real gap, stop and re-plan.

**Files:**
- No code changes expected.

- [ ] **Step 3.1: Trace the quota call sites for `FeatureGenerateRevisionPlan`**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
grep -n "DebitQuota\|debitCalls\|sqlDebitPlanCalls" pkg/aipipeline/*.go
grep -n "finalizeError\|finalizeSuccess" pkg/aipipeline/service_generation.go
```

Expected findings:
- `DebitQuota` is called only in `finalize` (`service_generation.go:218`), guarded by `r.emitted > 0`.
- `finalizeError` does NOT call `DebitQuota` — failures never debit, so there is nothing to refund.

- [ ] **Step 3.2: Confirm the empty-emit edge case**

If the AI streams zero valid `Day` items, `r.emitted == 0` and quota is not debited. That matches the spec's intent ("refund on failure"). No code change.

- [ ] **Step 3.3: Record the finding**

No file edit needed. The conclusion ("debit-on-success matches spec intent") is captured in Task 4's spec amendment.

---

## Task 4: Spec amendments (text only)

Update Spec B so its wording matches reality on three points where the code intentionally diverged: the `annales_image_id` column type, the `plan_calls` quota column name, the entitlement error code, and the quota debit pattern.

**Files:**
- Modify: `docs/superpowers/specs/2026-04-19-ai-revision-plan-design.md`

- [ ] **Step 4.1: Fix `annales_image_id` type in §4.1**

The spec says `BIGINT REFERENCES images(id)`, but `images.id` is `TEXT` (`db_sql/setup_core.go:41`). Change line 81 from:

```sql
    annales_image_id  BIGINT      REFERENCES images(id) ON DELETE SET NULL,
```

to:

```sql
    annales_image_id  TEXT        REFERENCES images(id) ON DELETE SET NULL,
```

- [ ] **Step 4.2: Fix the quota column name in §4.4**

The spec says `plan_used`. The implementation uses `plan_calls` for parity with `prompt_calls`, `pdf_calls`, `check_calls`. Change §4.4 from:

```
`ai_quota_daily` adds a `plan_used` column (default 0). No schema-breaking migration — existing rows default gracefully.
```

to:

```
`ai_quota_daily` adds `plan_calls` and `cross_subject_rank_calls` columns (default 0), parallel to the existing `prompt_calls` / `pdf_calls` / `check_calls` counters. No schema-breaking migration — existing rows default gracefully.
```

- [ ] **Step 4.3: Fix the entitlement error code in §7**

The spec says `403 ai_entitlement_required`; the implementation uses `402 no_ai_access` (consistent with billing's "Payment Required" semantics across the codebase, see `internal/httpx/errors.go:35,58`). In the §7 error table, change the first row from:

```
| User has no AI subscription | `403 ai_entitlement_required` (shared with Spec A). Frontend shows paywall. |
```

to:

```
| User has no AI subscription | `402 no_ai_access` (shared with Spec A). Frontend shows paywall. |
```

- [ ] **Step 4.4: Fix the quota debit pattern in §6.1**

The spec describes `Debit(user, "plan", 1) — refund on failure`. The implementation uses a debit-on-success pattern (no debit happens on failure, so there is nothing to refund). In §6.1, change:

```
→ aiQuotaService.Debit(user, "plan", 1)  — refund on failure
```

to:

```
→ pipeline debits `plan_calls` only on successful completion (post-stream); failures never debit, so no refund step is required
```

- [ ] **Step 4.5: Verify the spec changes render cleanly**

```bash
grep -n "BIGINT.*images\|plan_used\|ai_entitlement_required\|refund on failure" /Users/martonroux/Documents/WEB/studbud_3/backend/docs/superpowers/specs/2026-04-19-ai-revision-plan-design.md
```

Expected: no matches. If any of the four old strings still appears, repeat the corresponding edit.

- [ ] **Step 4.6: Commit**

```bash
git add docs/superpowers/specs/2026-04-19-ai-revision-plan-design.md
git commit -m "$(cat <<'EOF'
Spec B: align spec wording with implementation

[&] §4.1: annales_image_id is TEXT (images.id is TEXT, not BIGINT)
[&] §4.4: plan_calls + cross_subject_rank_calls (was plan_used)
[&] §6.1: debit-on-success (was debit + refund on failure)
[&] §7: 402 no_ai_access (was 403 ai_entitlement_required)
EOF
)"
```

---

## Final verification

- [ ] **Step F.1: Full test suite**

```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
ENV=test DATABASE_URL=postgres://localhost/studbud_test?sslmode=disable go test ./...
```

Expected: PASS.

- [ ] **Step F.2: Confirm git log shows three focused commits**

```bash
git log --oneline -3
```

Expected, top-to-bottom:
1. `Spec B: align spec wording with implementation`
2. `Spec B: persist ai_jobs id on revision_plans.generation_id`
3. `Spec B: idempotent plan schema + partial active-exams index`

- [ ] **Step F.3: Confirm no `replace` directive sneaked into go.mod**

```bash
grep -n "^replace " /Users/martonroux/Documents/WEB/studbud_3/backend/go.mod || echo "clean"
```

Expected: `clean`.

---

## Coverage check (self-review)

Each Spec B fidelity gap from the audit maps to a task:

| Gap | Task |
|-----|------|
| `annales_image_id` type mismatch | Task 4.1 (spec wins; code is correct) |
| Missing partial index `WHERE date >= CURRENT_DATE` | Task 1 (code change) |
| Missing `revision_plans.generation_id` column | Task 1 (schema) + Task 2 (wiring) |
| Destructive `DROP TABLE … CASCADE` boot preamble | Task 1 (code change) |
| Quota column `plan_calls` vs spec `plan_used` | Task 4.2 (spec wins) |
| Entitlement code `no_ai_access`/402 vs spec `ai_entitlement_required`/403 | Task 4.3 (spec wins) |
| Quota refund-on-failure trace | Task 3 (verify) + Task 4.4 (spec wording) |

No spec requirement is left unaddressed.
