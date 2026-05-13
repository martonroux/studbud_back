# Spec D Part 1 — Quiz Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Spec D quiz stubs with the real generate / play / retake flow. Build the AI pipeline integration (`FeatureGenerateQuiz`), reconcile the scaffolded schema with §2 of the design, and ship the standalone-quiz half of Spec D: `POST /quizzes/generate`, `POST /quizzes/:id/start`, `GET /quizzes/:id/attempts/:aid/resume`, `POST /quizzes/:id/attempts/:aid/answer`, `POST /quizzes/:id/attempts/:aid/abandon`, `POST /quizzes/:id/retake`, `GET /quizzes/:id/attempts/:aid`, `GET /quizzes/:id/history`. Excludes plan integration (covered by Plan D2) and sharing/quality (covered by Plan D3).

**Architecture:** Single backend package `pkg/quiz` owns all writes to `quizzes`, `quiz_questions`, `quiz_attempts`, `quiz_attempt_answers`. AI generation routes exclusively through `pkg/aipipeline.Service.RunStructuredGeneration` with a new `FeatureGenerateQuiz` key. Correctness lives server-side only — `correct_jsonb` never crosses the API boundary before submission. At most one in-progress attempt per `(quiz_id, user_id)` is enforced by a partial UNIQUE index. Question rows are immutable after insert; "fix a bad question" means regenerate.

**Tech Stack:** Go 1.25 + pgx + stdlib `net/http.ServeMux`; `pkg/aipipeline` for AI calls; Postgres for state.

**Spec reference:** `docs/superpowers/specs/2026-04-21-ai-quiz-design.md`.

**Prerequisite reading for the implementer:**
- Spec D design doc (above)
- `pkg/aipipeline/service_revision.go` — most recent precedent for adding a feature
- `pkg/aipipeline/model.go:10-26` — where `FeatureKey` constants live
- `pkg/aipipeline/quota.go` — pattern for per-feature daily caps
- `pkg/aipipeline/prompts.go` + `pkg/aipipeline/prompts/revision_plan.tmpl` — prompt template convention (`go:embed`)
- `pkg/plan/generate.go` — pattern for draining streaming AI chunks into domain rows
- `db_sql/setup_quiz.go` — existing scaffold (drift documented in Phase 1)
- `db_sql/setup_billing_test.go` — pattern for schema TDD (Spec C precedent)
- `cmd/app/routes.go:163-178` — current `/quiz/*` stub registration to replace
- `CLAUDE.md` (project root via `docs/CLAUDE.md`) — KISS, function-size rules, commit format

---

## Phase 1 — Schema reconciliation

`db_sql/setup_quiz.go` was scaffolded before Spec D §2 was finalised; the existing tables have substantial drift. Pre-launch (no production data) we destructively replace the schema in one transactional setup. Tests come first and assert the desired final shape — implementation iterates until they pass.

**Drift summary** (existing → spec):

| Table                    | Drift                                                                                                                                                                                                                                                          |
|--------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `quizzes`                | `owner_id`→`user_id`; `subject_id` NULL→NOT NULL; missing `chapter_id`, `source_share_token`, `card_pool_jsonb`, `settings_jsonb`, `question_count`, `model`, `prompt_hash`; remove `title`, `parent_quiz_id`, `duel_id`; `plan_id`→`source_plan_id`; CHECK source drops `'duel'`. |
| `quiz_questions`         | `position`→`ordinal`; `prompt`→`stem`; `choices`→`options_jsonb`; `correct_index` SMALLINT→`correct_jsonb` JSONB; missing `question_type`, `referenced_fc_ids_jsonb`; `source_flashcard_id` removed; add `UNIQUE (quiz_id, ordinal)`.                            |
| `quiz_attempts`          | `finished_at`→`completed_at`; missing `state`, `correct_count`, `total_count` NOT NULL, `score_pct`, `plan_id`, `plan_date`; add partial UNIQUE for `state='in_progress'`; add indexes.                                                                          |
| `quiz_attempt_answers`   | `chosen_index` SMALLINT→`user_answer_jsonb` JSONB; `is_correct`→`correct`. PK unchanged.                                                                                                                                                                        |
| `quiz_share_links`       | Add `expires_at`; covered fully by Plan D3 (left intact in D1).                                                                                                                                                                                                 |
| `quiz_quality_reports`   | Add `note`, add CHECK on `reason`; covered fully by Plan D3 (left intact in D1).                                                                                                                                                                                |

`ai_quota_daily.quiz_calls` and `ai_quota_daily.quiz_demo_used` are already present (`db_sql/setup_ai.go:36-37`) — no schema work needed here.

### Task 1: Schema test — desired post-migration shape

**Files:**
- Create: `db_sql/setup_quiz_test.go`

- [ ] **Step 1: Write the failing test**

```go
package db_sql

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/testutil"
)

// TestQuizSchema_MatchesSpec asserts the post-migration shape per Spec D §2.
// Runs against the shared test pool; the migration sequence in SetupAll is
// expected to leave the tables in the spec shape.
func TestQuizSchema_MatchesSpec(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	ctx := context.Background()

	// quizzes: required columns
	for _, col := range []string{
		"user_id", "subject_id", "chapter_id", "kind", "source",
		"source_plan_id", "source_share_token",
		"card_pool_jsonb", "settings_jsonb",
		"question_count", "model", "prompt_hash", "created_at",
	} {
		requireColumn(t, pool, "quizzes", col)
	}

	// quizzes: removed columns are gone
	for _, col := range []string{"owner_id", "title", "parent_quiz_id", "duel_id"} {
		requireColumnAbsent(t, pool, "quizzes", col)
	}

	// quizzes.subject_id must be NOT NULL
	requireNotNull(t, pool, "quizzes", "subject_id")

	// quizzes.kind CHECK accepts 'specific' and 'global'; quizzes.source accepts
	// 'user', 'plan', 'shared_copy' and rejects 'duel'.
	requireCheckAccepts(t, pool, `INSERT INTO quizzes (user_id, subject_id, kind, source, card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash) SELECT id, $1, 'specific', 'user', '[]'::jsonb, '{}'::jsonb, 0, 'm', 'h' FROM users LIMIT 1`)
	requireCheckRejects(t, pool, `INSERT INTO quizzes (user_id, subject_id, kind, source, card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash) SELECT id, $1, 'specific', 'duel', '[]'::jsonb, '{}'::jsonb, 0, 'm', 'h' FROM users LIMIT 1`)

	// quiz_questions: renamed/added columns
	for _, col := range []string{
		"ordinal", "question_type", "stem", "options_jsonb",
		"correct_jsonb", "referenced_fc_ids_jsonb", "explanation",
	} {
		requireColumn(t, pool, "quiz_questions", col)
	}
	for _, col := range []string{"position", "prompt", "choices", "correct_index", "source_flashcard_id"} {
		requireColumnAbsent(t, pool, "quiz_questions", col)
	}
	requireUniqueIndex(t, pool, "quiz_questions", []string{"quiz_id", "ordinal"})

	// quiz_attempts: state column + indexes
	for _, col := range []string{
		"state", "correct_count", "total_count", "score_pct",
		"completed_at", "plan_id", "plan_date",
	} {
		requireColumn(t, pool, "quiz_attempts", col)
	}
	requireColumnAbsent(t, pool, "quiz_attempts", "finished_at")
	requirePartialUniqueIndex(t, pool, "quiz_attempts",
		[]string{"quiz_id", "user_id"}, "state = 'in_progress'")

	// quiz_attempt_answers: renamed columns
	requireColumn(t, pool, "quiz_attempt_answers", "user_answer_jsonb")
	requireColumn(t, pool, "quiz_attempt_answers", "correct")
	for _, col := range []string{"chosen_index", "is_correct"} {
		requireColumnAbsent(t, pool, "quiz_attempt_answers", col)
	}
}

func requireColumn(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		    WHERE table_name = $1 AND column_name = $2)`, table, col).Scan(&exists)
	if err != nil {
		t.Fatalf("query column %s.%s: %v", table, col, err)
	}
	if !exists {
		t.Fatalf("column %s.%s missing", table, col)
	}
}

func requireColumnAbsent(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var exists bool
	_ = pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		    WHERE table_name = $1 AND column_name = $2)`, table, col).Scan(&exists)
	if exists {
		t.Fatalf("column %s.%s should not exist", table, col)
	}
}

func requireNotNull(t *testing.T, pool *pgxpool.Pool, table, col string) {
	t.Helper()
	var nullable string
	err := pool.QueryRow(context.Background(),
		`SELECT is_nullable FROM information_schema.columns
		   WHERE table_name = $1 AND column_name = $2`, table, col).Scan(&nullable)
	if err != nil {
		t.Fatalf("query nullable %s.%s: %v", table, col, err)
	}
	if nullable != "NO" {
		t.Fatalf("%s.%s is_nullable = %q, want NO", table, col, nullable)
	}
}

func requireUniqueIndex(t *testing.T, pool *pgxpool.Pool, table string, cols []string) {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = $1
		  AND indexdef ILIKE '%UNIQUE%'
		  AND `+columnsLike(cols), table).Scan(&n)
	if err != nil {
		t.Fatalf("query unique index on %s%v: %v", table, cols, err)
	}
	if n == 0 {
		t.Fatalf("no UNIQUE index on %s%v", table, cols)
	}
}

func requirePartialUniqueIndex(t *testing.T, pool *pgxpool.Pool, table string, cols []string, where string) {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = $1
		  AND indexdef ILIKE '%UNIQUE%'
		  AND indexdef ILIKE '%' || $2 || '%'
		  AND `+columnsLike(cols), table, where).Scan(&n)
	if err != nil {
		t.Fatalf("query partial unique on %s: %v", table, err)
	}
	if n == 0 {
		t.Fatalf("no partial UNIQUE on %s%v WHERE %s", table, cols, where)
	}
}

func columnsLike(cols []string) string {
	q := ""
	for i, c := range cols {
		if i > 0 {
			q += " AND "
		}
		q += "indexdef ILIKE '%" + c + "%'"
	}
	return q
}

func requireCheckAccepts(t *testing.T, pool *pgxpool.Pool, q string) {
	t.Helper()
	subjectID := seedSubject(t, pool)
	if _, err := pool.Exec(context.Background(), q, subjectID); err != nil {
		t.Fatalf("CHECK should have accepted; got %v", err)
	}
}

func requireCheckRejects(t *testing.T, pool *pgxpool.Pool, q string) {
	t.Helper()
	subjectID := seedSubject(t, pool)
	if _, err := pool.Exec(context.Background(), q, subjectID); err == nil {
		t.Fatalf("CHECK should have rejected; INSERT succeeded")
	}
}

// seedSubject inserts a minimal subject (and its required user) for CHECK probes.
func seedSubject(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	u := testutil.NewVerifiedUser(t, pool)
	var sid int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subjects (user_id, name) VALUES ($1, 'probe') RETURNING id`, u.ID,
	).Scan(&sid)
	if err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	return sid
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./db_sql/ -run TestQuizSchema_MatchesSpec -v
```

Expected: FAIL on the very first `requireColumn` for `quizzes.user_id` (current scaffold has `owner_id`).

- [ ] **Step 3: Commit the failing test**

```bash
git add db_sql/setup_quiz_test.go
git commit -m "$(cat <<'EOF'
Spec D: schema assertion test for quiz tables

[+] db_sql/setup_quiz_test.go asserts §2 shape (quizzes, quiz_questions, quiz_attempts, quiz_attempt_answers)
EOF
)"
```

- [ ] **Step 4: Lint**

```
go vet ./db_sql/...
```

Expected: clean.

### Task 2: Reconcile `db_sql/setup_quiz.go` to spec shape

This task replaces the body of `setup_quiz.go`. Pre-launch we DROP destructively (mirrors `setup_ai.go:47-50`'s pattern for `flashcard_keywords`). After this task the test from Task 1 should pass.

**Files:**
- Modify: `db_sql/setup_quiz.go` (full rewrite)

- [ ] **Step 1: Replace the file**

```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// quizSchema installs the Spec D §2 quiz tables. Pre-launch we destructively
// realign the scaffold; once the codebase has shipped this can move to pure
// CREATE TABLE IF NOT EXISTS + ALTER. The DROP ordering respects FKs (children
// before parents).
const quizSchema = `
DROP TABLE IF EXISTS quiz_quality_reports CASCADE;
DROP TABLE IF EXISTS quiz_sent_to_friends CASCADE;
DROP TABLE IF EXISTS quiz_share_links CASCADE;
DROP TABLE IF EXISTS quiz_attempt_answers CASCADE;
DROP TABLE IF EXISTS quiz_attempts CASCADE;
DROP TABLE IF EXISTS quiz_questions CASCADE;
DROP TABLE IF EXISTS quizzes CASCADE;

CREATE TABLE quizzes (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id          BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    chapter_id          BIGINT REFERENCES chapters(id) ON DELETE SET NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('specific','global')),
    source              TEXT NOT NULL CHECK (source IN ('user','plan','shared_copy')),
    source_plan_id      BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    source_share_token  TEXT,
    card_pool_jsonb     JSONB NOT NULL,
    settings_jsonb      JSONB NOT NULL,
    question_count      INT NOT NULL,
    model               TEXT NOT NULL,
    prompt_hash         TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quizzes_user_created  ON quizzes (user_id, created_at DESC);
CREATE INDEX idx_quizzes_subject       ON quizzes (subject_id);

CREATE TABLE quiz_questions (
    id                       BIGSERIAL PRIMARY KEY,
    quiz_id                  BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    ordinal                  INT NOT NULL,
    question_type            TEXT NOT NULL CHECK (question_type IN ('multi_choice','true_false','fill_blank')),
    stem                     TEXT NOT NULL,
    options_jsonb            JSONB,
    correct_jsonb            JSONB NOT NULL,
    explanation              TEXT,
    referenced_fc_ids_jsonb  JSONB NOT NULL,
    UNIQUE (quiz_id, ordinal)
);
CREATE INDEX idx_quiz_questions_quiz ON quiz_questions (quiz_id);

CREATE TABLE quiz_attempts (
    id            BIGSERIAL PRIMARY KEY,
    quiz_id       BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state         TEXT NOT NULL CHECK (state IN ('in_progress','completed','abandoned')),
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ,
    correct_count INT NOT NULL DEFAULT 0,
    total_count   INT NOT NULL,
    score_pct     INT,
    plan_id       BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    plan_date     DATE
);
CREATE INDEX idx_quiz_attempts_user_started ON quiz_attempts (user_id, started_at DESC);
CREATE INDEX idx_quiz_attempts_quiz_state   ON quiz_attempts (quiz_id, state);
CREATE UNIQUE INDEX uniq_quiz_attempts_in_progress
    ON quiz_attempts (quiz_id, user_id) WHERE state = 'in_progress';

CREATE TABLE quiz_attempt_answers (
    attempt_id        BIGINT NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id       BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_answer_jsonb JSONB NOT NULL,
    correct           BOOLEAN NOT NULL,
    answered_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attempt_id, question_id)
);

CREATE TABLE quiz_share_links (
    token       TEXT PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_quiz_share_links_quiz ON quiz_share_links (quiz_id);

CREATE TABLE quiz_sent_to_friends (
    quiz_id      BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);

CREATE TABLE quiz_quality_reports (
    id          BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL CHECK (reason IN ('wrong_answer','bad_distractors','unclear','off_topic','other')),
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quiz_quality_reports_question ON quiz_quality_reports (question_id);
`

// setupQuiz installs the Spec D quiz schema. Idempotent across cold boots
// (DROPs are guarded by IF EXISTS; CREATEs run once per fresh DB).
func setupQuiz(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, quizSchema); err != nil {
		return fmt.Errorf("exec quiz schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Run the schema test**

```
go test ./db_sql/ -run TestQuizSchema_MatchesSpec -v
```

Expected: PASS. (You may need to drop the test DB so setup re-runs from scratch: `dropdb studbud_test && createdb studbud_test`, or `make test-reset` if the Makefile defines it.)

- [ ] **Step 3: Run the broader db_sql suite**

```
go test ./db_sql/... -v
```

Expected: PASS for all existing schema tests; no leftover assertions on the old quiz columns.

- [ ] **Step 4: Lint**

```
go vet ./db_sql/...
gofmt -l db_sql/
```

Expected: clean. If `gofmt -l` lists files, run `gofmt -w db_sql/` and re-stage.

- [ ] **Step 5: Commit**

```bash
git add db_sql/setup_quiz.go
git commit -m "$(cat <<'EOF'
Spec D: reconcile quiz schema to design §2

[&] quizzes: owner_id->user_id; +chapter_id, source_share_token, card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash
[-] quizzes: title, parent_quiz_id, duel_id removed; 'duel' dropped from source CHECK
[&] quiz_questions: position->ordinal, prompt->stem, choices->options_jsonb, correct_index->correct_jsonb; +question_type, referenced_fc_ids_jsonb, UNIQUE(quiz_id, ordinal)
[&] quiz_attempts: finished_at->completed_at; +state, correct_count, total_count, score_pct, plan_id, plan_date; partial UNIQUE on in_progress
[&] quiz_attempt_answers: chosen_index->user_answer_jsonb, is_correct->correct
[+] quiz_share_links.expires_at
[+] quiz_quality_reports.note + reason CHECK constraint
[&] pre-launch destructive DROP+CREATE per setup_ai.go precedent
EOF
)"
```

### Task 3: Sweep callers for stale column references

The drift renamed many columns. Any Go file that referenced the old names will fail to compile or pass tests. Sweep them now so subsequent phases start clean.

**Files:**
- Modify (potentially): `pkg/quiz/stub.go`, `api/handler/quiz_stub.go`, any other touching `owner_id`, `chosen_index`, `is_correct`, `position` (within quiz context), `finished_at` (within quiz context), `prompt` (within quiz context), `choices`, `correct_index`, `source_flashcard_id`.

- [ ] **Step 1: Grep for offenders**

```
grep -rn --include='*.go' -E "owner_id|chosen_index|is_correct|source_flashcard_id|correct_index" . | grep -v vendor | grep -i quiz || true
grep -rn --include='*.go' -E "quiz.*\.position|\.finished_at|quiz.*\.choices|quiz.*\.prompt[^_]" . | grep -v vendor || true
```

Expected: matches in `pkg/quiz/stub.go` and `api/handler/quiz_stub.go` only — both are about to be rewritten in later phases. Existing matches outside the quiz domain (e.g., `friendships.owner_id` analogues, plan code's `position`) are unrelated; review each match and leave foreign-domain ones alone.

- [ ] **Step 2: Run the full backend suite**

```
make test
```

Expected: PASS. The schema test from Task 1 plus all existing tests should pass. If a non-quiz test fails, it almost certainly references a column name that *coincidentally* matched in the grep — investigate that specific failure rather than mass-renaming.

- [ ] **Step 3: Commit (only if Step 1 surfaced fixes outside quiz stubs)**

```bash
git add -p
git commit -m "$(cat <<'EOF'
Spec D: sweep stale quiz column references

[!] update non-stub references to renamed quiz columns
EOF
)"
```

(If no fixes needed, skip the commit.)

---

## Phase 2 — AI pipeline integration

Spec D §1 requires "all AI calls go through `RunStructuredGeneration`". This phase adds the `FeatureGenerateQuiz` key, daily quota plumbing, prompt template, and the per-feature service method that wraps the pipeline.

### Task 4: Add `FeatureGenerateQuiz` constant + `QuizCalls` quota field

**Files:**
- Modify: `pkg/aipipeline/model.go:13-26` (add constant), `pkg/aipipeline/model.go:75-92` (add field + default)
- Test: `pkg/aipipeline/model_test.go` (new file)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./pkg/aipipeline/ -run TestFeatureGenerateQuiz -v
go test ./pkg/aipipeline/ -run TestDefaultQuotaLimits_HasQuizCalls -v
```

Expected: BUILD FAIL (`FeatureGenerateQuiz` undefined) and FAIL on the field lookup.

- [ ] **Step 3: Add the constant and field**

In `pkg/aipipeline/model.go`, after the existing `FeatureCrossSubjectRank` line (line 25):

```go
	// FeatureGenerateQuiz is the on-demand quiz generator (Spec D).
	FeatureGenerateQuiz FeatureKey = "generate_quiz"
```

In `pkg/aipipeline/model.go` extend `QuotaLimits` (lines 75-81):

```go
// QuotaLimits holds per-feature daily caps.
type QuotaLimits struct {
	PromptCalls int // PromptCalls is the daily cap on successful prompt generations
	PDFCalls    int // PDFCalls is the daily cap on successful PDF generations
	PDFPages    int // PDFPages is the daily cap on total PDF pages consumed
	CheckCalls  int // CheckCalls is the daily cap on successful check calls
	PlanCalls   int // PlanCalls caps daily revision-plan generations (default 5)
	QuizCalls   int // QuizCalls caps daily quiz generations (default 10)
}
```

And in `DefaultQuotaLimits()` (lines 84-92):

```go
func DefaultQuotaLimits() QuotaLimits {
	return QuotaLimits{
		PromptCalls: 20,
		PDFCalls:    5,
		PDFPages:    100,
		CheckCalls:  50,
		PlanCalls:   5,
		QuizCalls:   10,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/aipipeline/ -run 'TestFeatureGenerateQuiz|TestDefaultQuotaLimits_HasQuizCalls' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/model.go pkg/aipipeline/model_test.go
git commit -m "$(cat <<'EOF'
Spec D: FeatureGenerateQuiz + QuizCalls quota

[+] FeatureGenerateQuiz feature key
[+] QuotaLimits.QuizCalls (default 10)
[+] model_test.go covers the constant + default
EOF
)"
```

### Task 5: Add quiz quota cases to `pkg/aipipeline/quota.go` + queries

The quota service must:
1. Reject `FeatureGenerateQuiz` calls when today's `quiz_calls` >= limit.
2. Debit one unit on successful generation.

Both live in `quota.go` / `queries.go`.

**Files:**
- Read first: `pkg/aipipeline/quota.go` (full), `pkg/aipipeline/queries.go` (full)
- Modify: `pkg/aipipeline/quota.go` (`checkAgainstLimitsPure` switch ~lines 72-97; `DebitQuota` dispatch), `pkg/aipipeline/queries.go` (add `sqlDebitQuizCalls`; extend `sqlSelectQuotaRow` if it lists columns; extend the upsert/ensure SQL)
- Test: `pkg/aipipeline/quota_test.go` (add cases)

- [ ] **Step 1: Write failing tests**

Append to `pkg/aipipeline/quota_test.go`:

```go
func TestCheckQuota_QuizCallsAllowsUnderLimit(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := newServiceForQuotaTest(t, pool, aipipeline.QuotaLimits{QuizCalls: 3})
	if err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 0); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckQuota_QuizCallsRejectsAtLimit(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.SeedQuotaAt(t, pool, u.ID, "quiz_calls", 3)

	svc := newServiceForQuotaTest(t, pool, aipipeline.QuotaLimits{QuizCalls: 3})
	err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 0)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("expected ErrQuotaExhausted, got %v", err)
	}
}

func TestDebitQuota_QuizCallsIncrementsRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := newServiceForQuotaTest(t, pool, aipipeline.QuotaLimits{QuizCalls: 5})
	if err := svc.DebitQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 1, 0); err != nil {
		t.Fatalf("debit: %v", err)
	}

	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT quiz_calls FROM ai_quota_daily WHERE user_id = $1 AND day = CURRENT_DATE`, u.ID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatalf("quiz_calls = %d, want 1", n)
	}
}
```

If `newServiceForQuotaTest` does not exist, model it after the existing helper used by the other quota tests (`pkg/aipipeline/quota_test.go` already contains the pattern — reuse the same constructor).

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./pkg/aipipeline/ -run 'TestCheckQuota_QuizCalls|TestDebitQuota_QuizCalls' -v
```

Expected: FAIL — `checkAgainstLimitsPure` has no `FeatureGenerateQuiz` case (returns nil today), and the debit SQL doesn't know how to bump `quiz_calls`.

- [ ] **Step 3: Add quota cases**

In `pkg/aipipeline/quota.go`, inside `checkAgainstLimitsPure` (the switch that begins around line 72), add a new case after `FeatureGenerateRevisionPlan`:

```go
	case FeatureGenerateQuiz:
		if used["quiz_calls"] >= limits.QuizCalls {
			return quotaExhausted("quiz")
		}
```

In the same file (or wherever the debit dispatcher lives — find it via `grep -n "DebitQuota" pkg/aipipeline/*.go`), add a case mapping `FeatureGenerateQuiz` to the new SQL constant.

In `pkg/aipipeline/queries.go`, add:

```go
// sqlDebitQuizCalls bumps quiz_calls by $2 (always 1) for today, upserting
// the daily row if absent.
const sqlDebitQuizCalls = `
INSERT INTO ai_quota_daily (user_id, day, quiz_calls)
VALUES ($1, CURRENT_DATE, $2)
ON CONFLICT (user_id, day)
DO UPDATE SET quiz_calls = ai_quota_daily.quiz_calls + EXCLUDED.quiz_calls
`
```

Also extend the SELECT that loads the daily row so it returns `quiz_calls` (and `quiz_demo_used` if not already), and update the `quotaRow` struct + `Scan` call chain. Mirror the existing `plan_calls` plumbing 1:1.

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/aipipeline/ -run 'TestCheckQuota_QuizCalls|TestDebitQuota_QuizCalls' -v
```

Expected: PASS.

- [ ] **Step 5: Run the full pipeline suite + lint**

```
go test ./pkg/aipipeline/... -v
go vet ./pkg/aipipeline/...
```

Expected: PASS; no vet diagnostics.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/quota.go pkg/aipipeline/queries.go pkg/aipipeline/quota_test.go
git commit -m "$(cat <<'EOF'
Spec D: quiz_calls quota plumbing

[+] checkAgainstLimitsPure case for FeatureGenerateQuiz
[+] sqlDebitQuizCalls upsert SQL
[&] quotaRow + select scan extended with quiz_calls
[+] quota tests: under-limit, at-limit, debit increments
EOF
)"
```

### Task 6: Add `prompts/generate_quiz.tmpl` + `RenderGenerateQuiz`

The pipeline expects each feature to expose a Render helper that takes domain inputs and returns the assembled prompt body. Mirror `RenderRevisionPlan` (`pkg/aipipeline/prompts.go:198`-ish) and `prompts/revision_plan.tmpl`.

**Files:**
- Create: `pkg/aipipeline/prompts/generate_quiz.tmpl`
- Modify: `pkg/aipipeline/prompts.go` (add `QuizGenValues` struct + `RenderGenerateQuiz` function)
- Test: `pkg/aipipeline/prompts_test.go` (add cases)

- [ ] **Step 1: Write failing tests**

Append to `pkg/aipipeline/prompts_test.go`:

```go
func TestRenderGenerateQuiz_IncludesAllInputs(t *testing.T) {
	body, err := aipipeline.RenderGenerateQuiz(aipipeline.QuizGenValues{
		SubjectName:    "Cellular Biology",
		Kind:           "specific",
		Size:           10,
		Types:          []string{"multi_choice", "true_false"},
		Cards: []aipipeline.QuizSourceCard{
			{ID: 42, Title: "Mitochondria", Question: "What is the powerhouse?", Answer: "Mitochondrion"},
			{ID: 43, Title: "Ribosomes",     Question: "What synthesises protein?", Answer: "Ribosomes"},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Cellular Biology",
		"multi_choice",
		"true_false",
		"Mitochondria",
		"Ribosomes",
		"10 quiz",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered prompt missing %q\n---\n%s\n---", want, body)
		}
	}
}

func TestRenderGenerateQuiz_GlobalKind_OmitsCards(t *testing.T) {
	body, err := aipipeline.RenderGenerateQuiz(aipipeline.QuizGenValues{
		SubjectName: "World History",
		Kind:        "global",
		Size:        5,
		Types:       []string{"multi_choice"},
		Cards:       nil,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(body, "global") {
		t.Fatalf("missing 'global' marker; got:\n%s", body)
	}
	if strings.Contains(body, "SOURCE CARDS") {
		t.Fatalf("global kind should not list SOURCE CARDS; got:\n%s", body)
	}
}
```

Add the necessary import for `strings` if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./pkg/aipipeline/ -run 'TestRenderGenerateQuiz' -v
```

Expected: BUILD FAIL — undefined `RenderGenerateQuiz`, `QuizGenValues`, `QuizSourceCard`.

- [ ] **Step 3: Add the template**

Create `pkg/aipipeline/prompts/generate_quiz.tmpl`:

```
You are the StudBud quiz generator. Produce {{ .Size }} quiz questions for the user's subject "{{ .SubjectName }}".

Quiz kind: {{ .Kind }} ({{ if eq .Kind "specific" }}questions must be answerable from the SOURCE CARDS below; do not invent facts outside them.{{ else }}questions test general subject knowledge, not specific cards.{{ end }})

Allowed question types: {{ range $i, $t := .Types }}{{ if $i }}, {{ end }}{{ $t }}{{ end }}.

For each question:
- choose one of the allowed types
- "multi_choice": 4 options, exactly one correct; distractors plausible but unambiguously wrong
- "true_false": stem is an assertion; correctValue is true or false
- "fill_blank": stem ends with "____"; accepted lists 1-3 surface forms (case + diacritic variants)

Output an array of {{ .Size }} quiz questions. Each element:
{
  "questionType": "multi_choice" | "true_false" | "fill_blank",
  "stem": "...",
  "options": ["...", "...", "...", "..."],   // multi_choice only; omit otherwise
  "correctIndex": 2,                          // multi_choice only
  "correctValue": true,                       // true_false only
  "accepted": ["mitochondrion", "Mitochondria"], // fill_blank only
  "explanation": "...",                       // optional, <=200 chars
  "referencedFcIds": [42]                     // ids of cards from SOURCE CARDS this question draws on; [] for global
}

{{ if eq .Kind "specific" }}SOURCE CARDS:
{{ range .Cards }}- [{{ .ID }}] {{ .Title }}: Q={{ .Question }} A={{ .Answer }}
{{ end }}{{ end }}
You are generating {{ .Size }} quiz questions. Do not exceed that count.
```

- [ ] **Step 4: Add the Render helper**

In `pkg/aipipeline/prompts.go`, after `RenderRevisionPlan`, add:

```go
// QuizSourceCard is one user-owned flashcard handed to the quiz generator
// when the quiz kind is "specific". Empty for "global" quizzes.
type QuizSourceCard struct {
	ID       int64  // ID is the flashcard primary key
	Title    string // Title is the flashcard title
	Question string // Question is the flashcard question body
	Answer   string // Answer is the flashcard correct answer
}

// QuizGenValues is the template input for the generate-quiz prompt.
type QuizGenValues struct {
	SubjectName string           // SubjectName labels the subject for the prompt
	Kind        string           // Kind is "specific" or "global"
	Size        int              // Size is the requested number of questions
	Types       []string         // Types lists allowed question types (subset of multi_choice|true_false|fill_blank)
	Cards       []QuizSourceCard // Cards is the source pool for kind="specific"; nil for "global"
}

// RenderGenerateQuiz returns the rendered prompt body for FeatureGenerateQuiz.
func RenderGenerateQuiz(v QuizGenValues) (string, error) {
	return renderTemplate("prompts/generate_quiz.tmpl", v)
}
```

- [ ] **Step 5: Run tests to verify they pass**

```
go test ./pkg/aipipeline/ -run 'TestRenderGenerateQuiz' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/prompts/generate_quiz.tmpl pkg/aipipeline/prompts.go pkg/aipipeline/prompts_test.go
git commit -m "$(cat <<'EOF'
Spec D: generate-quiz prompt template

[+] prompts/generate_quiz.tmpl with multi_choice/true_false/fill_blank shape
[+] QuizGenValues, QuizSourceCard, RenderGenerateQuiz
[+] prompts_test.go: render covers specific + global kinds
EOF
)"
```

### Task 7: Add `pkg/aipipeline/service_generate_quiz.go`

The domain caller (next phase's `pkg/quiz` service) drives the pipeline through a typed wrapper. Mirror `service_revision.go`'s `GenerateRevisionPlan`.

**Files:**
- Create: `pkg/aipipeline/service_generate_quiz.go`
- Test: `pkg/aipipeline/service_generate_quiz_test.go`

- [ ] **Step 1: Write the failing test**

```go
package aipipeline_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestGenerateQuiz_StreamsQuestionsAndDebitsQuota(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"What is X?","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[]}]}`},
			{Done: true},
		},
	}
	svc := aipipeline.NewService(pool, fake,
		testutil.AccessSvc(t, pool),
		aipipeline.QuotaLimits{QuizCalls: 5},
		"claude-test")

	out, err := svc.GenerateQuiz(context.Background(), aipipeline.QuizGenerateInput{
		UserID:    u.ID,
		SubjectID: sid,
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
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	fake := &testutil.FakeAIClient{}
	svc := aipipeline.NewService(pool, fake,
		testutil.AccessSvc(t, pool),
		aipipeline.DefaultQuotaLimits(),
		"claude-test")

	_, err := svc.GenerateQuiz(context.Background(), aipipeline.QuizGenerateInput{
		UserID: u.ID, SubjectID: sid, Prompt: "x",
	})
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("want ErrNoAIAccess, got %v", err)
	}
}
```

(Use the existing `testutil.NewSubject` helper if it exists; if not, create a tiny helper alongside the test that inserts a subject and returns its id. Same for `testutil.AccessSvc`.)

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./pkg/aipipeline/ -run 'TestGenerateQuiz_' -v
```

Expected: BUILD FAIL — `GenerateQuiz` undefined.

- [ ] **Step 3: Create the service file**

`pkg/aipipeline/service_generate_quiz.go`:

```go
package aipipeline

import (
	"context"

	"studbud/backend/internal/aiProvider"
)

// QuizGenerateInput is the typed entry point for FeatureGenerateQuiz.
type QuizGenerateInput struct {
	UserID    int64                  // UserID owns the generation
	SubjectID int64                  // SubjectID anchors the prompt + quota check
	Prompt    string                 // Prompt is the rendered template body (see RenderGenerateQuiz)
	Metadata  map[string]any         // Metadata is forwarded into ai_jobs.metadata for debugging
	Images    []aiProvider.ImagePart // Images is always empty for quiz (kept for symmetry with other features)
}

// QuizGenerateOutput is the typed stream + job handle returned to callers.
type QuizGenerateOutput struct {
	Chunks <-chan AIChunk // Chunks is the streaming validation channel
	JobID  int64          // JobID is the ai_jobs row this run is associated with
}

// GenerateQuiz wraps RunStructuredGeneration with the FeatureGenerateQuiz feature key.
func (s *Service) GenerateQuiz(ctx context.Context, in QuizGenerateInput) (*QuizGenerateOutput, error) {
	req := AIRequest{
		UserID:    in.UserID,
		Feature:   FeatureGenerateQuiz,
		SubjectID: in.SubjectID,
		Prompt:    in.Prompt,
		Metadata:  in.Metadata,
	}
	ch, jobID, err := s.RunStructuredGeneration(ctx, req)
	if err != nil {
		return nil, err
	}
	return &QuizGenerateOutput{Chunks: ch, JobID: jobID}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/aipipeline/ -run 'TestGenerateQuiz_' -v
```

Expected: PASS.

- [ ] **Step 5: Lint**

```
go vet ./pkg/aipipeline/...
gofmt -l pkg/aipipeline/
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/service_generate_quiz.go pkg/aipipeline/service_generate_quiz_test.go
git commit -m "$(cat <<'EOF'
Spec D: pipeline wrapper GenerateQuiz

[+] service_generate_quiz.go: QuizGenerateInput/Output + GenerateQuiz
[+] tests cover quota debit + entitlement rejection
EOF
)"
```

---

## Phase 3 — `pkg/quiz` domain service

The pipeline emits validated question JSON objects on a channel. `pkg/quiz` drains the channel, resolves the card pool, persists `quizzes` + `quiz_questions` transactionally, and owns the play/score state machine.

### Task 8: Replace `pkg/quiz/stub.go` with a real package skeleton

The stub's `Service` only has `Generate`/`Attempt`/`Share` placeholders returning `ErrNotImplemented`. We replace it with the domain types Spec D §2-3 needs.

**Files:**
- Delete: `pkg/quiz/stub.go`
- Create: `pkg/quiz/model.go` (types)
- Create: `pkg/quiz/service.go` (struct + constructor; methods filled in later tasks)
- Test: `pkg/quiz/service_test.go` (skeleton ping test)

- [ ] **Step 1: Read what currently consumes the stub**

```
grep -rn "pkg/quiz\b\|quiz\.Service\|quiz\.NewService\|quiz\.Generate\|quiz\.Attempt\|quiz\.Share" --include='*.go' .
```

Expected consumers: `api/handler/quiz_stub.go`, `cmd/app/deps.go` (or wherever `d.quiz` is constructed), `cmd/app/routes.go:165`. Phase 4 will rewrite these; for now we keep the public surface (`NewService`, `Generate`, `Attempt`, `Share`) compatible enough to compile.

- [ ] **Step 2: Write the skeleton test**

`pkg/quiz/service_test.go`:

```go
package quiz_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/quiz"
)

func TestNewService_AllowsZeroAIDependency(t *testing.T) {
	// Constructor must accept a nil AI service for tests that only exercise
	// non-generation methods (Start/Answer/Retake/Results).
	var pool *pgxpool.Pool // unused for this smoke
	svc := quiz.NewService(pool, nil)
	if svc == nil {
		t.Fatal("nil service")
	}
	_ = context.Background()
}
```

- [ ] **Step 3: Delete the stub and write the skeleton**

Delete `pkg/quiz/stub.go`. Create `pkg/quiz/model.go`:

```go
package quiz

import (
	"encoding/json"
	"time"
)

// Kind is the high-level mode of the quiz.
type Kind string

const (
	// KindSpecific draws questions from a specific subset of the user's flashcards.
	KindSpecific Kind = "specific"
	// KindGlobal draws questions from general subject knowledge (no card grounding).
	KindGlobal Kind = "global"
)

// Source records who/what generated the quiz row.
type Source string

const (
	// SourceUser is a user-initiated standalone quiz.
	SourceUser Source = "user"
	// SourcePlan is a revision-plan-materialised quiz (Spec D2).
	SourcePlan Source = "plan"
	// SourceSharedCopy is a clone produced by accepting a share link (Spec D3).
	SourceSharedCopy Source = "shared_copy"
)

// QuestionType enumerates the supported quiz question shapes.
type QuestionType string

const (
	QTypeMultiChoice QuestionType = "multi_choice"
	QTypeTrueFalse   QuestionType = "true_false"
	QTypeFillBlank   QuestionType = "fill_blank"
)

// CardFilter narrows the eligible flashcard pool for Kind="specific".
type CardFilter string

const (
	FilterAll    CardFilter = "all"     // FilterAll uses every flashcard in the chapter/subject
	FilterBadOK  CardFilter = "bad_ok"  // FilterBadOK includes only flashcards whose last result was Bad or OK
	FilterDue    CardFilter = "due"     // FilterDue uses cards whose dueHeuristic fires today
)

// GenerateRequest is the input to Service.Generate.
type GenerateRequest struct {
	UserID      int64
	SubjectID   int64
	ChapterID   *int64         // nil = whole subject
	Kind        Kind
	Size        int            // 5/10/15/20
	Types       []QuestionType
	CardFilter  CardFilter     // specific only
	PlanContext *PlanContext   // non-nil iff invoked from a plan slot (Spec D2)
}

// PlanContext is the per-call carrier for plan-materialised quizzes (Spec D2 fills this in).
type PlanContext struct {
	PlanID    int64
	PlanDate  string // YYYY-MM-DD
	SlotIndex int
}

// GenerateResult is the output of Service.Generate.
type GenerateResult struct {
	QuizID        int64
	QuestionCount int
	Kind          Kind
}

// Quiz is the persisted projection of a quizzes row.
type Quiz struct {
	ID             int64
	UserID         int64
	SubjectID      int64
	ChapterID      *int64
	Kind           Kind
	Source         Source
	SourcePlanID   *int64
	QuestionCount  int
	Model          string
	CreatedAt      time.Time
}

// Question is the persisted projection of a quiz_questions row.
// CorrectJSON is loaded server-side only and never returned to the play API.
type Question struct {
	ID                 int64
	QuizID             int64
	Ordinal            int
	Type               QuestionType
	Stem               string
	Options            json.RawMessage // null for non-MCQ
	CorrectJSON        json.RawMessage // server-only
	Explanation        string
	ReferencedFcIDs    []int64
}

// PublicQuestion is the play-facing projection — strips CorrectJSON.
type PublicQuestion struct {
	ID      int64           `json:"id"`
	Ordinal int             `json:"ordinal"`
	Type    QuestionType    `json:"type"`
	Stem    string          `json:"stem"`
	Options json.RawMessage `json:"options,omitempty"`
}

// AttemptState enumerates the lifecycle of a play attempt.
type AttemptState string

const (
	StateInProgress AttemptState = "in_progress"
	StateCompleted  AttemptState = "completed"
	StateAbandoned  AttemptState = "abandoned"
)

// Attempt is the persisted projection of a quiz_attempts row.
type Attempt struct {
	ID           int64
	QuizID       int64
	UserID       int64
	State        AttemptState
	StartedAt    time.Time
	CompletedAt  *time.Time
	CorrectCount int
	TotalCount   int
	ScorePct     *int
	PlanID       *int64
	PlanDate     *string
}

// Progress is the {answered, total} pill shown during play.
type Progress struct {
	Answered int `json:"answered"`
	Total    int `json:"total"`
}
```

Create `pkg/quiz/service.go`:

```go
package quiz

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/aipipeline"
)

// AIDriver is the slice of the AI pipeline this package depends on.
// Defined as an interface so tests can swap in a fake.
type AIDriver interface {
	GenerateQuiz(ctx pkgctx, in aipipeline.QuizGenerateInput) (*aipipeline.QuizGenerateOutput, error)
}

// pkgctx is local shorthand to keep imports tight; actual signature uses context.Context.
type pkgctx = context.Context

// Service is the domain-level quiz facade.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
	ai AIDriver      // ai produces quiz questions; may be nil for non-generation methods
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, ai AIDriver) *Service {
	return &Service{db: db, ai: ai}
}
```

(If `pkgctx` aliasing is unidiomatic in this codebase, expand to `context.Context` directly and import `context`. The actual codebase uses the latter — match the surrounding style.)

- [ ] **Step 4: Update `cmd/app/deps.go` (or equivalent) to pass the AI service**

Locate where `d.quiz` is currently constructed (`grep -n "quiz\.NewService\|quiz\.New(" cmd/`). The current line passes only `d.db`; extend it to `quiz.NewService(d.db, d.ai)` (the `*aipipeline.Service` already lives on `deps`).

The handler `NewQuizHandler(d.quiz)` keeps the same constructor signature, so no other changes here yet — Phase 4 will rewrite the handler.

- [ ] **Step 5: Build the whole module**

```
go build ./...
```

Expected: clean. If `aipipeline.Service` doesn't satisfy `AIDriver` due to method signature differences, adjust the interface definition to match `(ctx context.Context, in aipipeline.QuizGenerateInput) (*aipipeline.QuizGenerateOutput, error)` exactly.

- [ ] **Step 6: Run the skeleton test + full quiz suite**

```
go test ./pkg/quiz/... -v
```

Expected: PASS for `TestNewService_AllowsZeroAIDependency`.

- [ ] **Step 7: Commit**

```bash
git add pkg/quiz/ cmd/app/deps.go
git rm pkg/quiz/stub.go
git commit -m "$(cat <<'EOF'
Spec D: pkg/quiz skeleton (model + service)

[-] pkg/quiz/stub.go
[+] pkg/quiz/model.go: Kind, Source, QuestionType, CardFilter, GenerateRequest/Result, Quiz, Question, Attempt, Progress
[+] pkg/quiz/service.go: AIDriver interface + Service struct + NewService
[&] cmd/app/deps.go: pass AI service into quiz.NewService
EOF
)"
```

### Task 9: Card pool resolver

`Generate` needs to materialise the flashcard pool before calling the AI. Specific quizzes draw from the user's flashcards filtered by `CardFilter`; global quizzes use an empty pool. The resolver returns both the typed pool (for prompt rendering) and the id-only snapshot (for `card_pool_jsonb`).

**Files:**
- Create: `pkg/quiz/pool.go`
- Test: `pkg/quiz/pool_test.go`

- [ ] **Step 1: Write failing tests**

`pkg/quiz/pool_test.go`:

```go
package quiz_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestResolveCardPool_Specific_All(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	testutil.NewFlashcard(t, pool, c1, "Mitochondria", "What is X?", "Mitochondrion")
	testutil.NewFlashcard(t, pool, c1, "Ribosomes", "What synth?", "Ribosomes")

	svc := quiz.NewService(pool, nil)
	cards, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sid, Kind: quiz.KindSpecific, CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cards) != 2 || len(ids) != 2 {
		t.Fatalf("got %d cards / %d ids, want 2/2", len(cards), len(ids))
	}
}

func TestResolveCardPool_Global_Empty(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Hist")

	svc := quiz.NewService(pool, nil)
	cards, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sid, Kind: quiz.KindGlobal,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cards) != 0 || len(ids) != 0 {
		t.Fatalf("global kind should not materialise cards; got %d/%d", len(cards), len(ids))
	}
}

func TestResolveCardPool_ChapterScoped(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	c2 := testutil.NewChapter(t, pool, sid, "C2")
	testutil.NewFlashcard(t, pool, c1, "X", "qx", "ax")
	testutil.NewFlashcard(t, pool, c2, "Y", "qy", "ay")

	svc := quiz.NewService(pool, nil)
	_, ids, err := svc.ResolvePoolForTest(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sid, ChapterID: &c1,
		Kind: quiz.KindSpecific, CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("chapter scoping returned %d cards, want 1", len(ids))
	}
}
```

If `testutil.NewChapter` / `NewFlashcard` helpers don't exist, add them under `testutil/seed.go` (small `INSERT … RETURNING id` helpers); the test code shows their signatures.

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./pkg/quiz/ -run 'TestResolveCardPool' -v
```

Expected: BUILD FAIL — `ResolvePoolForTest` undefined.

- [ ] **Step 3: Implement the resolver**

`pkg/quiz/pool.go`:

```go
package quiz

import (
	"context"
	"fmt"

	"studbud/backend/pkg/aipipeline"
)

// resolveCardPool materialises the flashcard pool for the generation request.
// Returns (typedCards, ids); both empty for KindGlobal.
func (s *Service) resolveCardPool(ctx context.Context, req GenerateRequest) ([]aipipeline.QuizSourceCard, []int64, error) {
	if req.Kind == KindGlobal {
		return nil, nil, nil
	}
	rows, err := s.db.Query(ctx, poolQuery(req), poolArgs(req)...)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve pool:\n%w", err)
	}
	defer rows.Close()

	var cards []aipipeline.QuizSourceCard
	var ids []int64
	for rows.Next() {
		var c aipipeline.QuizSourceCard
		if err := rows.Scan(&c.ID, &c.Title, &c.Question, &c.Answer); err != nil {
			return nil, nil, fmt.Errorf("scan card:\n%w", err)
		}
		cards = append(cards, c)
		ids = append(ids, c.ID)
	}
	return cards, ids, rows.Err()
}

// poolQuery builds the SELECT for the requested filter. Chapter scoping is
// optional. The query joins flashcards → chapters → subjects so subject
// ownership is enforced (defends against cross-tenant card leakage).
func poolQuery(req GenerateRequest) string {
	q := `
SELECT f.id, f.title, f.question, f.answer
  FROM flashcards f
  JOIN chapters c ON c.id = f.chapter_id
 WHERE c.subject_id = $1`
	if req.ChapterID != nil {
		q += ` AND c.id = $2`
	}
	switch req.CardFilter {
	case FilterBadOK:
		q += ` AND f.last_result IN ('bad','ok')`
	case FilterDue:
		q += ` AND f.due_at <= now()`
	}
	q += ` ORDER BY f.id`
	return q
}

func poolArgs(req GenerateRequest) []any {
	args := []any{req.SubjectID}
	if req.ChapterID != nil {
		args = append(args, *req.ChapterID)
	}
	return args
}

// ResolvePoolForTest exposes resolveCardPool for tests.
// Production callers must use Generate.
func (s *Service) ResolvePoolForTest(ctx context.Context, req GenerateRequest) ([]aipipeline.QuizSourceCard, []int64, error) {
	return s.resolveCardPool(ctx, req)
}
```

(If `flashcards.last_result` / `due_at` columns are named differently in this codebase, adjust the SQL to match. `grep -n "last_result\|due_at" db_sql/setup_core.go` will confirm.)

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestResolveCardPool' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/pool.go pkg/quiz/pool_test.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: card pool resolver

[+] resolveCardPool: subject/chapter scoping + FilterAll|BadOK|Due
[+] tests cover specific/all, global/empty, chapter scoping
[+] testutil seed helpers for chapters + flashcards
EOF
)"
```

### Task 10: Persist quiz + questions transactionally

After the AI stream emits all validated questions, the service writes one `quizzes` row + N `quiz_questions` rows in a single transaction. Question rows are immutable after insert.

**Files:**
- Create: `pkg/quiz/persist.go`
- Test: `pkg/quiz/persist_test.go`

- [ ] **Step 1: Write failing tests**

`pkg/quiz/persist_test.go`:

```go
package quiz_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestPersistQuiz_WritesQuizAndQuestionsTransactionally(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	qs := []quiz.RawQuestion{
		{
			Type: quiz.QTypeMultiChoice, Stem: "What is X?",
			Options: json.RawMessage(`["A","B","C","D"]`),
			Correct: json.RawMessage(`{"index":2}`),
			ReferencedFcIDs: []int64{},
		},
		{
			Type: quiz.QTypeTrueFalse, Stem: "Earth is round",
			Correct: json.RawMessage(`{"value":true}`),
			ReferencedFcIDs: []int64{},
		},
	}
	svc := quiz.NewService(pool, nil)
	id, err := svc.PersistQuizForTest(context.Background(), quiz.PersistInput{
		UserID:        u.ID,
		SubjectID:     sid,
		Kind:          quiz.KindGlobal,
		Source:        quiz.SourceUser,
		CardPool:      []int64{},
		Settings:      json.RawMessage(`{"size":2}`),
		Model:         "claude-test",
		PromptHash:    "h",
		Questions:     qs,
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Assert row counts.
	var qc int
	_ = pool.QueryRow(context.Background(),
		`SELECT question_count FROM quizzes WHERE id=$1`, id).Scan(&qc)
	if qc != 2 {
		t.Fatalf("question_count = %d, want 2", qc)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_questions WHERE quiz_id=$1`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("quiz_questions rows = %d, want 2", n)
	}

	// Assert ordinal sequence.
	rows, _ := pool.Query(context.Background(),
		`SELECT ordinal FROM quiz_questions WHERE quiz_id=$1 ORDER BY ordinal`, id)
	defer rows.Close()
	want := []int{1, 2}
	var got []int
	for rows.Next() {
		var o int
		_ = rows.Scan(&o)
		got = append(got, o)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ordinals = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Expected: BUILD FAIL — `PersistQuizForTest`, `PersistInput`, `RawQuestion` undefined.

- [ ] **Step 3: Implement `persist.go`**

```go
package quiz

import (
	"context"
	"encoding/json"
	"fmt"
)

// RawQuestion is the AI-emitted question shape after validation, ready for insert.
type RawQuestion struct {
	Type            QuestionType
	Stem            string
	Options         json.RawMessage // nil for non-MCQ
	Correct         json.RawMessage // {"index":N} | {"value":bool} | {"accepted":[...]}
	Explanation     string
	ReferencedFcIDs []int64
}

// PersistInput is the input to persistQuiz.
type PersistInput struct {
	UserID            int64
	SubjectID         int64
	ChapterID         *int64
	Kind              Kind
	Source            Source
	SourcePlanID      *int64
	SourceShareToken  *string
	CardPool          []int64
	Settings          json.RawMessage
	Model             string
	PromptHash        string
	Questions         []RawQuestion
}

// persistQuiz writes one quizzes row + N quiz_questions rows in one transaction.
// Returns the new quiz id.
func (s *Service) persistQuiz(ctx context.Context, in PersistInput) (int64, error) {
	cardPoolJSON, err := json.Marshal(in.CardPool)
	if err != nil {
		return 0, fmt.Errorf("marshal card pool:\n%w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var quizID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO quizzes (
			user_id, subject_id, chapter_id, kind, source,
			source_plan_id, source_share_token,
			card_pool_jsonb, settings_jsonb,
			question_count, model, prompt_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		in.UserID, in.SubjectID, in.ChapterID, string(in.Kind), string(in.Source),
		in.SourcePlanID, in.SourceShareToken,
		cardPoolJSON, in.Settings,
		len(in.Questions), in.Model, in.PromptHash,
	).Scan(&quizID)
	if err != nil {
		return 0, fmt.Errorf("insert quiz:\n%w", err)
	}

	for i, q := range in.Questions {
		fcIDs, err := json.Marshal(q.ReferencedFcIDs)
		if err != nil {
			return 0, fmt.Errorf("marshal fc ids:\n%w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO quiz_questions (
				quiz_id, ordinal, question_type, stem,
				options_jsonb, correct_jsonb, explanation, referenced_fc_ids_jsonb
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			quizID, i+1, string(q.Type), q.Stem,
			q.Options, q.Correct, nullableString(q.Explanation), fcIDs,
		)
		if err != nil {
			return 0, fmt.Errorf("insert question %d:\n%w", i+1, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx:\n%w", err)
	}
	return quizID, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PersistQuizForTest exposes persistQuiz to tests.
func (s *Service) PersistQuizForTest(ctx context.Context, in PersistInput) (int64, error) {
	return s.persistQuiz(ctx, in)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestPersistQuiz' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/persist.go pkg/quiz/persist_test.go
git commit -m "$(cat <<'EOF'
Spec D: transactional quiz persistence

[+] persistQuiz: quizzes + quiz_questions in one tx with ordinal sequencing
[+] PersistInput, RawQuestion types
[+] persist_test.go: row counts, ordinal contiguity
EOF
)"
```

### Task 11: `Generate` — orchestrate pool → AI → persist

`Service.Generate` ties together the pool resolver, prompt rendering, AI pipeline call, chunk validation, and persistence. The handler-facing return is just the quiz id.

**Files:**
- Create: `pkg/quiz/generate.go`
- Test: `pkg/quiz/generate_test.go`

- [ ] **Step 1: Write failing test**

`pkg/quiz/generate_test.go`:

```go
package quiz_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestGenerate_HappyPath_SpecificMultiChoice(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	testutil.NewFlashcard(t, pool, c1, "Mito", "What?", "Mitochondrion")

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"What is X?","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[1]}]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")
	svc := quiz.NewService(pool, ai)

	res, err := svc.Generate(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sid, Kind: quiz.KindSpecific,
		Size: 1, Types: []quiz.QuestionType{quiz.QTypeMultiChoice},
		CardFilter: quiz.FilterAll,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.QuestionCount != 1 {
		t.Fatalf("got %d, want 1", res.QuestionCount)
	}

	// Quota debited
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT quiz_calls FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE`, u.ID,
	).Scan(&n)
	if n != 1 {
		t.Fatalf("quiz_calls = %d, want 1", n)
	}
}

func TestGenerate_RejectsInvalidSize(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	svc := quiz.NewService(pool, nil)
	_, err := svc.Generate(context.Background(), quiz.GenerateRequest{
		UserID: u.ID, SubjectID: sid, Kind: quiz.KindGlobal,
		Size: 99, Types: []quiz.QuestionType{quiz.QTypeMultiChoice},
	})
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL — `Service.Generate` undefined.

- [ ] **Step 3: Implement `generate.go`**

```go
package quiz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// allowedSizes is the v1 white-list for quiz size (Spec D §4 Setup).
var allowedSizes = map[int]bool{5: true, 10: true, 15: true, 20: true}

// Generate produces a quiz from a flashcard pool + AI call + persistence.
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := validateRequest(req); err != nil {
		return GenerateResult{}, err
	}

	cards, ids, err := s.resolveCardPool(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	if req.Kind == KindGlobal && len(cards) > 0 {
		return GenerateResult{}, fmt.Errorf("internal: global kind returned cards") // defence-in-depth
	}

	subjectName, err := s.lookupSubjectName(ctx, req.SubjectID, req.UserID)
	if err != nil {
		return GenerateResult{}, err
	}

	body, err := aipipeline.RenderGenerateQuiz(aipipeline.QuizGenValues{
		SubjectName: subjectName,
		Kind:        string(req.Kind),
		Size:        req.Size,
		Types:       typeStrings(req.Types),
		Cards:       cards,
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("render prompt:\n%w", err)
	}

	out, err := s.ai.GenerateQuiz(ctx, aipipeline.QuizGenerateInput{
		UserID: req.UserID, SubjectID: req.SubjectID, Prompt: body,
		Metadata: map[string]any{
			"kind": string(req.Kind), "size": req.Size, "types": typeStrings(req.Types),
		},
	})
	if err != nil {
		return GenerateResult{}, err
	}

	questions, err := drainQuestions(out.Chunks, req.Size)
	if err != nil {
		return GenerateResult{}, err
	}

	settings, _ := json.Marshal(map[string]any{
		"size":  req.Size,
		"types": typeStrings(req.Types),
	})

	quizID, err := s.persistQuiz(ctx, PersistInput{
		UserID:    req.UserID,
		SubjectID: req.SubjectID,
		ChapterID: req.ChapterID,
		Kind:      req.Kind,
		Source:    SourceUser, // D2 will set SourcePlan when PlanContext != nil
		CardPool:  ids,
		Settings:  settings,
		Model:     "claude-test", // production: thread Service.model through; see Task 7 wrapper if Plan D2 needs it differently
		PromptHash: hashPrompt(body),
		Questions: questions,
	})
	if err != nil {
		return GenerateResult{}, err
	}

	return GenerateResult{QuizID: quizID, QuestionCount: len(questions), Kind: req.Kind}, nil
}

func validateRequest(req GenerateRequest) error {
	if req.Kind != KindSpecific && req.Kind != KindGlobal {
		return fmt.Errorf("%w: kind=%q", myErrors.ErrValidation, req.Kind)
	}
	if !allowedSizes[req.Size] {
		return fmt.Errorf("%w: size=%d (allowed: 5,10,15,20)", myErrors.ErrValidation, req.Size)
	}
	if len(req.Types) == 0 {
		return fmt.Errorf("%w: types must be non-empty", myErrors.ErrValidation)
	}
	for _, t := range req.Types {
		if t != QTypeMultiChoice && t != QTypeTrueFalse && t != QTypeFillBlank {
			return fmt.Errorf("%w: type=%q", myErrors.ErrValidation, t)
		}
	}
	return nil
}

func typeStrings(ts []QuestionType) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}

func hashPrompt(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

func (s *Service) lookupSubjectName(ctx context.Context, sid, uid int64) (string, error) {
	var name string
	err := s.db.QueryRow(ctx,
		`SELECT name FROM subjects WHERE id = $1 AND user_id = $2`, sid, uid,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("lookup subject:\n%w", err)
	}
	return name, nil
}
```

Also create `pkg/quiz/drain.go`:

```go
package quiz

import (
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// drainQuestions consumes the AI chunk channel into RawQuestion rows.
// Validates each item; rejects the run if the total count != wantSize.
func drainQuestions(ch <-chan aipipeline.AIChunk, wantSize int) ([]RawQuestion, error) {
	var out []RawQuestion
	for chunk := range ch {
		switch chunk.Kind {
		case aipipeline.ChunkItem:
			rq, err := decodeItem(chunk.Item)
			if err != nil {
				return nil, err
			}
			out = append(out, rq)
		case aipipeline.ChunkError:
			return nil, chunk.Err
		}
	}
	if len(out) != wantSize {
		return nil, fmt.Errorf("%w: AI returned %d items, want %d",
			myErrors.ErrAIProvider, len(out), wantSize)
	}
	return out, nil
}

type rawItem struct {
	QuestionType    string          `json:"questionType"`
	Stem            string          `json:"stem"`
	Options         json.RawMessage `json:"options,omitempty"`
	CorrectIndex    *int            `json:"correctIndex,omitempty"`
	CorrectValue    *bool           `json:"correctValue,omitempty"`
	Accepted        []string        `json:"accepted,omitempty"`
	Explanation     string          `json:"explanation,omitempty"`
	ReferencedFcIDs []int64         `json:"referencedFcIds"`
}

func decodeItem(raw json.RawMessage) (RawQuestion, error) {
	var item rawItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return RawQuestion{}, fmt.Errorf("%w: %s", myErrors.ErrAIProvider, err)
	}
	rq := RawQuestion{
		Type:            QuestionType(item.QuestionType),
		Stem:            item.Stem,
		Explanation:     item.Explanation,
		ReferencedFcIDs: item.ReferencedFcIDs,
	}
	switch rq.Type {
	case QTypeMultiChoice:
		if item.CorrectIndex == nil || item.Options == nil {
			return RawQuestion{}, fmt.Errorf("%w: MCQ missing options/correctIndex", myErrors.ErrAIProvider)
		}
		rq.Options = item.Options
		correct, _ := json.Marshal(map[string]any{"index": *item.CorrectIndex})
		rq.Correct = correct
	case QTypeTrueFalse:
		if item.CorrectValue == nil {
			return RawQuestion{}, fmt.Errorf("%w: T/F missing correctValue", myErrors.ErrAIProvider)
		}
		correct, _ := json.Marshal(map[string]any{"value": *item.CorrectValue})
		rq.Correct = correct
	case QTypeFillBlank:
		if len(item.Accepted) == 0 {
			return RawQuestion{}, fmt.Errorf("%w: fill_blank missing accepted[]", myErrors.ErrAIProvider)
		}
		correct, _ := json.Marshal(map[string]any{"accepted": item.Accepted})
		rq.Correct = correct
	default:
		return RawQuestion{}, fmt.Errorf("%w: unknown questionType %q", myErrors.ErrAIProvider, item.QuestionType)
	}
	return rq, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestGenerate' -v
```

Expected: PASS.

- [ ] **Step 5: Lint + vet**

```
go vet ./pkg/quiz/...
gofmt -l pkg/quiz/
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add pkg/quiz/generate.go pkg/quiz/drain.go pkg/quiz/generate_test.go
git commit -m "$(cat <<'EOF'
Spec D: Service.Generate orchestrates pool->AI->persist

[+] generate.go: validateRequest, lookupSubjectName, prompt render, AI call, persist
[+] drain.go: decodeItem typed per QuestionType, count match enforcement
[+] generate_test.go: happy path MCQ, invalid size rejection
EOF
)"
```

### Task 12: `Start` — idempotent attempt creation

Spec D §3 Play: `POST /quizzes/:id/start` returns the existing `in_progress` attempt or creates a new one. The partial UNIQUE index from Phase 1 enforces single-in-progress.

**Files:**
- Create: `pkg/quiz/attempt.go` (will grow over Tasks 12-16)
- Test: `pkg/quiz/attempt_test.go`

- [ ] **Step 1: Write failing test**

```go
package quiz_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestStart_CreatesAttempt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 3) // 3 questions

	svc := quiz.NewService(pool, nil)
	att, next, prog, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if att.ID == 0 {
		t.Fatalf("attempt id zero")
	}
	if att.State != quiz.StateInProgress {
		t.Fatalf("state = %q, want in_progress", att.State)
	}
	if next == nil || next.Ordinal != 1 {
		t.Fatalf("next.Ordinal = %v, want 1", next)
	}
	if prog.Answered != 0 || prog.Total != 3 {
		t.Fatalf("progress = %+v, want 0/3", prog)
	}
}

func TestStart_IdempotentReturnsExistingInProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	a1, _, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	a2, _, _, err := svc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("Start returned different attempts: %d vs %d", a1.ID, a2.ID)
	}
}
```

If `testutil.NewQuiz` doesn't exist, add a helper that inserts one quiz row + N MCQ questions and returns the quiz id (the signature is shown in the test).

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL — `Service.Start` undefined.

- [ ] **Step 3: Implement `Start`**

`pkg/quiz/attempt.go`:

```go
package quiz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"studbud/backend/internal/myErrors"
)

// Start returns the user's in-progress attempt for the quiz, creating one if none exists.
// Returns (attempt, nextQuestion, progress).
func (s *Service) Start(ctx context.Context, uid, quizID int64) (Attempt, *PublicQuestion, Progress, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return Attempt{}, nil, Progress{}, err
	}
	att, err := s.findInProgress(ctx, uid, quizID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, nil, Progress{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		att, err = s.createAttempt(ctx, uid, quizID)
		if err != nil {
			return Attempt{}, nil, Progress{}, err
		}
	}
	next, prog, err := s.advance(ctx, att.ID)
	if err != nil {
		return Attempt{}, nil, Progress{}, err
	}
	return att, next, prog, nil
}

// requireQuizOwner returns ErrForbidden if quizID is not owned by uid.
func (s *Service) requireQuizOwner(ctx context.Context, uid, quizID int64) error {
	var owner int64
	err := s.db.QueryRow(ctx, `SELECT user_id FROM quizzes WHERE id=$1`, quizID).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return myErrors.ErrNotFound
		}
		return fmt.Errorf("lookup quiz owner:\n%w", err)
	}
	if owner != uid {
		return myErrors.ErrForbidden
	}
	return nil
}

func (s *Service) findInProgress(ctx context.Context, uid, quizID int64) (Attempt, error) {
	var att Attempt
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts
		 WHERE quiz_id=$1 AND user_id=$2 AND state='in_progress'`,
		quizID, uid,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	return att, err
}

func (s *Service) createAttempt(ctx context.Context, uid, quizID int64) (Attempt, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT question_count FROM quizzes WHERE id=$1`, quizID).Scan(&total)
	if err != nil {
		return Attempt{}, fmt.Errorf("lookup question_count:\n%w", err)
	}
	var att Attempt
	err = s.db.QueryRow(ctx, `
		INSERT INTO quiz_attempts (quiz_id, user_id, state, total_count)
		VALUES ($1,$2,'in_progress',$3)
		RETURNING id, quiz_id, user_id, state, started_at, completed_at,
		          correct_count, total_count, score_pct, plan_id, plan_date`,
		quizID, uid, total,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	if err != nil {
		return Attempt{}, fmt.Errorf("insert attempt:\n%w", err)
	}
	return att, nil
}

// advance returns the next unanswered question + the current progress pill.
// Returns (nil, progress, nil) when every question is answered.
func (s *Service) advance(ctx context.Context, attemptID int64) (*PublicQuestion, Progress, error) {
	var prog Progress
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1),
		       (SELECT total_count FROM quiz_attempts WHERE id=$1)`,
		attemptID,
	).Scan(&prog.Answered, &prog.Total)
	if err != nil {
		return nil, prog, fmt.Errorf("progress:\n%w", err)
	}
	if prog.Answered >= prog.Total {
		return nil, prog, nil
	}
	var q PublicQuestion
	var opts []byte
	err = s.db.QueryRow(ctx, `
		SELECT qq.id, qq.ordinal, qq.question_type, qq.stem, qq.options_jsonb
		  FROM quiz_questions qq
		  JOIN quiz_attempts qa ON qa.quiz_id = qq.quiz_id
		 WHERE qa.id = $1
		   AND qq.id NOT IN (SELECT question_id FROM quiz_attempt_answers WHERE attempt_id=$1)
		 ORDER BY qq.ordinal
		 LIMIT 1`, attemptID,
	).Scan(&q.ID, &q.Ordinal, &q.Type, &q.Stem, &opts)
	if err != nil {
		return nil, prog, fmt.Errorf("next question:\n%w", err)
	}
	if opts != nil {
		q.Options = json.RawMessage(opts)
	}
	return &q, prog, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestStart' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/attempt.go pkg/quiz/attempt_test.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: Service.Start (idempotent attempt creation)

[+] Start: creates or returns in-progress attempt; emits next + progress
[+] requireQuizOwner, findInProgress, createAttempt, advance helpers
[+] PublicQuestion strips correct_jsonb at the boundary
[+] tests cover create-then-idempotent
EOF
)"
```

### Task 13: `Answer` — score + commit-on-answer

`POST /quizzes/:id/attempts/:aid/answer` scores the answer server-side, inserts `quiz_attempt_answers`, updates `correct_count`, and on the last question sets `state='completed'`. Plan-progress writeback ships in D2.

**Files:**
- Modify: `pkg/quiz/attempt.go` (add `Answer`, `Abandon`, `score*` helpers)
- Test: `pkg/quiz/attempt_test.go` (add cases)

- [ ] **Step 1: Write failing tests**

```go
func TestAnswer_MCQCorrect(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2) // 2 MCQ questions, both correct_index=2

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)

	res, err := svc.Answer(context.Background(), u.ID, att.ID,
		q1.ID, json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !res.Correct {
		t.Fatalf("Correct = false, want true")
	}
	if res.Next == nil || res.Next.Ordinal != 2 {
		t.Fatalf("Next.Ordinal = %v, want 2", res.Next)
	}
}

func TestAnswer_LastQuestion_CompletesAttempt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)

	res, err := svc.Answer(context.Background(), u.ID, att.ID,
		q1.ID, json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Next != nil {
		t.Fatalf("Next should be nil on final question")
	}
	var state string
	var pct *int
	_ = pool.QueryRow(context.Background(),
		`SELECT state, score_pct FROM quiz_attempts WHERE id=$1`, att.ID,
	).Scan(&state, &pct)
	if state != "completed" || pct == nil || *pct != 100 {
		t.Fatalf("post-answer: state=%q pct=%v, want completed/100", state, pct)
	}
}

func TestAnswer_DoubleSubmit_NoOp(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	_, err := svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":0}`))
	// Idempotent per Spec D §5.7: PK (attempt_id, question_id) ON CONFLICT DO NOTHING.
	if err != nil {
		t.Fatalf("double-submit returned error: %v", err)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1`, att.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("got %d answer rows, want 1 (idempotent)", n)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement `Answer` + scoring**

Append to `pkg/quiz/attempt.go`:

```go
// AnswerResult is the response payload for /answer.
type AnswerResult struct {
	Correct        bool             `json:"correct"`
	CorrectAnswer  json.RawMessage  `json:"correctAnswer"`
	Explanation    string           `json:"explanation,omitempty"`
	Next           *PublicQuestion  `json:"nextQuestion,omitempty"`
}

// Answer scores the user's submission and advances the attempt. Idempotent on (attempt_id, question_id).
func (s *Service) Answer(ctx context.Context, uid, attemptID, questionID int64, userAns json.RawMessage) (AnswerResult, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return AnswerResult{}, err
	}
	if att.UserID != uid {
		return AnswerResult{}, myErrors.ErrForbidden
	}
	if att.State != StateInProgress {
		return AnswerResult{}, fmt.Errorf("%w: attempt not in_progress", myErrors.ErrConflict)
	}
	q, err := s.loadQuestion(ctx, questionID, att.QuizID)
	if err != nil {
		return AnswerResult{}, err
	}
	correct, err := scoreAnswer(q.Type, q.CorrectJSON, userAns)
	if err != nil {
		return AnswerResult{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return AnswerResult{}, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO quiz_attempt_answers (attempt_id, question_id, user_answer_jsonb, correct)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (attempt_id, question_id) DO NOTHING`,
		attemptID, questionID, userAns, correct)
	if err != nil {
		return AnswerResult{}, fmt.Errorf("insert answer:\n%w", err)
	}
	inserted := tag.RowsAffected() > 0
	if inserted && correct {
		if _, err := tx.Exec(ctx,
			`UPDATE quiz_attempts SET correct_count = correct_count + 1 WHERE id=$1`,
			attemptID); err != nil {
			return AnswerResult{}, fmt.Errorf("bump correct_count:\n%w", err)
		}
	}

	var answered int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM quiz_attempt_answers WHERE attempt_id=$1`, attemptID,
	).Scan(&answered); err != nil {
		return AnswerResult{}, err
	}
	if answered >= att.TotalCount {
		if err := s.completeAttempt(ctx, tx, attemptID); err != nil {
			return AnswerResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AnswerResult{}, fmt.Errorf("commit:\n%w", err)
	}

	next, _, err := s.advance(ctx, attemptID)
	if err != nil {
		return AnswerResult{}, err
	}
	return AnswerResult{
		Correct:       correct,
		CorrectAnswer: q.CorrectJSON,
		Explanation:   q.Explanation,
		Next:          next,
	}, nil
}

func (s *Service) loadAttempt(ctx context.Context, id int64) (Attempt, error) {
	var att Attempt
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts WHERE id=$1`, id,
	).Scan(&att.ID, &att.QuizID, &att.UserID, &att.State, &att.StartedAt, &att.CompletedAt,
		&att.CorrectCount, &att.TotalCount, &att.ScorePct, &att.PlanID, &att.PlanDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return att, myErrors.ErrNotFound
	}
	if err != nil {
		return att, fmt.Errorf("load attempt:\n%w", err)
	}
	return att, nil
}

func (s *Service) loadQuestion(ctx context.Context, qid, quizID int64) (Question, error) {
	var q Question
	var opts, fcIDs []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, quiz_id, ordinal, question_type, stem,
		       options_jsonb, correct_jsonb, COALESCE(explanation,''), referenced_fc_ids_jsonb
		  FROM quiz_questions WHERE id=$1 AND quiz_id=$2`,
		qid, quizID,
	).Scan(&q.ID, &q.QuizID, &q.Ordinal, &q.Type, &q.Stem, &opts, &q.CorrectJSON, &q.Explanation, &fcIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, myErrors.ErrNotFound
	}
	if err != nil {
		return q, fmt.Errorf("load question:\n%w", err)
	}
	if opts != nil {
		q.Options = json.RawMessage(opts)
	}
	if len(fcIDs) > 0 {
		_ = json.Unmarshal(fcIDs, &q.ReferencedFcIDs)
	}
	return q, nil
}

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
	return nil
}
```

And create `pkg/quiz/score.go`:

```go
package quiz

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"studbud/backend/internal/myErrors"
)

// scoreAnswer returns true if userAns matches the correct payload.
// MCQ: {"index": N}; T/F: {"value": bool}; fill_blank: {"value": "text"} compared against accepted[].
func scoreAnswer(t QuestionType, correct, user json.RawMessage) (bool, error) {
	switch t {
	case QTypeMultiChoice:
		var c struct{ Index int `json:"index"` }
		var u struct{ Index int `json:"index"` }
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, fmt.Errorf("bad correct payload:\n%w", err)
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		return c.Index == u.Index, nil
	case QTypeTrueFalse:
		var c struct{ Value bool `json:"value"` }
		var u struct{ Value bool `json:"value"` }
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, err
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		return c.Value == u.Value, nil
	case QTypeFillBlank:
		var c struct{ Accepted []string `json:"accepted"` }
		var u struct{ Value string `json:"value"` }
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, err
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		uNorm := normalizeFillBlank(u.Value)
		for _, a := range c.Accepted {
			if normalizeFillBlank(a) == uNorm {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: unknown question type %q", myErrors.ErrInvalidInput, t)
}

func normalizeFillBlank(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

Add a test for `normalizeFillBlank` in `pkg/quiz/score_test.go`:

```go
package quiz

import "testing"

func TestNormalizeFillBlank_StripsCaseSpaceAndPunctuation(t *testing.T) {
	cases := []struct{ a, b string }{
		{"Mitochondria.", "mitochondria"},
		{"  the  Cell ", "the  cell"}, // intentional inner-double-space — accepted variants should match
		{"Reagan,", "reagan"},
	}
	for _, c := range cases {
		if got := normalizeFillBlank(c.a); got != c.b {
			t.Fatalf("normalize(%q) = %q, want %q", c.a, got, c.b)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestAnswer|TestNormalizeFillBlank' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/attempt.go pkg/quiz/score.go pkg/quiz/score_test.go pkg/quiz/attempt_test.go
git commit -m "$(cat <<'EOF'
Spec D: Service.Answer (score + commit-on-answer)

[+] Answer scores server-side, inserts answer row idempotently, bumps correct_count
[+] Final answer flips attempt to state='completed' and computes score_pct
[+] score.go: MCQ/T-F/fill_blank with normalized fuzzy compare
[+] tests: MCQ correct, last-question completion, double-submit idempotency
EOF
)"
```

### Task 14: `Abandon` + `Retake`

**Files:**
- Modify: `pkg/quiz/attempt.go`
- Test: `pkg/quiz/attempt_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestAbandon_FreesInProgressSlot(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	a1, _, _, _ := svc.Start(context.Background(), u.ID, qid)
	if err := svc.Abandon(context.Background(), u.ID, a1.ID); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	// A retake (= new attempt) should now succeed.
	a2, err := svc.Retake(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("retake after abandon: %v", err)
	}
	if a2.ID == a1.ID {
		t.Fatalf("retake should not reuse abandoned attempt")
	}
}

func TestRetake_BlockedWhileInProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	_, _, _, _ = svc.Start(context.Background(), u.ID, qid)
	_, err := svc.Retake(context.Background(), u.ID, qid)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement**

Append to `pkg/quiz/attempt.go`:

```go
// Abandon marks an in-progress attempt as abandoned, freeing the unique slot.
// No-op for already-completed attempts.
func (s *Service) Abandon(ctx context.Context, uid, attemptID int64) error {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	if att.UserID != uid {
		return myErrors.ErrForbidden
	}
	if att.State == StateCompleted {
		return nil
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE quiz_attempts SET state='abandoned' WHERE id=$1 AND state='in_progress'`,
		attemptID); err != nil {
		return fmt.Errorf("abandon:\n%w", err)
	}
	return nil
}

// Retake creates a fresh attempt over the same questions. 409 if an
// in-progress attempt already exists.
func (s *Service) Retake(ctx context.Context, uid, quizID int64) (Attempt, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return Attempt{}, err
	}
	_, err := s.findInProgress(ctx, uid, quizID)
	if err == nil {
		return Attempt{}, fmt.Errorf("%w: in-progress attempt exists", myErrors.ErrConflict)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, err
	}
	return s.createAttempt(ctx, uid, quizID)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestAbandon|TestRetake' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/attempt.go pkg/quiz/attempt_test.go
git commit -m "$(cat <<'EOF'
Spec D: Service.Abandon + Service.Retake

[+] Abandon frees the partial-unique in_progress slot
[+] Retake creates a fresh attempt; 409 if in-progress
EOF
)"
```

### Task 15: `Results` + `History`

**Files:**
- Create: `pkg/quiz/results.go`
- Test: `pkg/quiz/results_test.go`

- [ ] **Step 1: Write failing test**

```go
package quiz_test

import (
	"context"
	"encoding/json"
	"testing"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestResults_FullReviewPayload(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2) // both MCQ index=2

	svc := quiz.NewService(pool, nil)
	att, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	r1, _ := svc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	_ = r1
	// Fetch next and answer the final.
	_, next, _, _ := svc.Start(context.Background(), u.ID, qid) // idempotent reload
	_, _ = svc.Answer(context.Background(), u.ID, att.ID, next.ID,
		json.RawMessage(`{"index":0}`))

	out, err := svc.GetAttempt(context.Background(), u.ID, att.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if out.Attempt.State != quiz.StateCompleted {
		t.Fatalf("state = %q, want completed", out.Attempt.State)
	}
	if len(out.Questions) != 2 {
		t.Fatalf("got %d Q rows, want 2", len(out.Questions))
	}
	if out.Questions[0].Correct == nil || !*out.Questions[0].Correct {
		t.Fatalf("Q1 should be marked correct")
	}
}

func TestHistory_ListsAllAttemptsForQuizByUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	svc := quiz.NewService(pool, nil)
	// First attempt + complete.
	att1, q1, _, _ := svc.Start(context.Background(), u.ID, qid)
	_, _ = svc.Answer(context.Background(), u.ID, att1.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	// Second attempt via Retake.
	_, _ = svc.Retake(context.Background(), u.ID, qid)

	hist, err := svc.History(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("got %d, want 2", len(hist))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL — `GetAttempt`, `History` undefined.

- [ ] **Step 3: Implement**

`pkg/quiz/results.go`:

```go
package quiz

import (
	"context"
	"encoding/json"
	"fmt"
)

// QuestionReview is one row in the results screen.
type QuestionReview struct {
	ID            int64           `json:"id"`
	Ordinal       int             `json:"ordinal"`
	Type          QuestionType    `json:"type"`
	Stem          string          `json:"stem"`
	Options       json.RawMessage `json:"options,omitempty"`
	UserAnswer    json.RawMessage `json:"userAnswer,omitempty"`
	CorrectAnswer json.RawMessage `json:"correctAnswer"`
	Explanation   string          `json:"explanation,omitempty"`
	Correct       *bool           `json:"correct,omitempty"`
}

// AttemptView is the GET /quizzes/:id/attempts/:aid response.
type AttemptView struct {
	Attempt   Attempt          `json:"attempt"`
	Questions []QuestionReview `json:"questions"`
}

// GetAttempt returns the full review payload (score, per-question outcome).
func (s *Service) GetAttempt(ctx context.Context, uid, attemptID int64) (AttemptView, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return AttemptView{}, err
	}
	if att.UserID != uid {
		return AttemptView{}, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, `
		SELECT qq.id, qq.ordinal, qq.question_type, qq.stem,
		       qq.options_jsonb, qq.correct_jsonb, COALESCE(qq.explanation,''),
		       qaa.user_answer_jsonb, qaa.correct
		  FROM quiz_questions qq
		  LEFT JOIN quiz_attempt_answers qaa
		    ON qaa.question_id = qq.id AND qaa.attempt_id = $1
		 WHERE qq.quiz_id = $2
		 ORDER BY qq.ordinal`,
		attemptID, att.QuizID,
	)
	if err != nil {
		return AttemptView{}, fmt.Errorf("review query:\n%w", err)
	}
	defer rows.Close()
	var qs []QuestionReview
	for rows.Next() {
		var r QuestionReview
		var opts, ans []byte
		var correct *bool
		if err := rows.Scan(&r.ID, &r.Ordinal, &r.Type, &r.Stem,
			&opts, &r.CorrectAnswer, &r.Explanation, &ans, &correct); err != nil {
			return AttemptView{}, err
		}
		if opts != nil {
			r.Options = json.RawMessage(opts)
		}
		if ans != nil {
			r.UserAnswer = json.RawMessage(ans)
		}
		r.Correct = correct
		qs = append(qs, r)
	}
	return AttemptView{Attempt: att, Questions: qs}, rows.Err()
}

// History returns every attempt this user has made on this quiz.
func (s *Service) History(ctx context.Context, uid, quizID int64) ([]Attempt, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, quiz_id, user_id, state, started_at, completed_at,
		       correct_count, total_count, score_pct, plan_id, plan_date
		  FROM quiz_attempts
		 WHERE quiz_id=$1 AND user_id=$2
		 ORDER BY started_at DESC`, quizID, uid)
	if err != nil {
		return nil, fmt.Errorf("history query:\n%w", err)
	}
	defer rows.Close()
	var atts []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.QuizID, &a.UserID, &a.State, &a.StartedAt, &a.CompletedAt,
			&a.CorrectCount, &a.TotalCount, &a.ScorePct, &a.PlanID, &a.PlanDate); err != nil {
			return nil, err
		}
		atts = append(atts, a)
	}
	return atts, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestResults|TestHistory' -v
```

Expected: PASS.

- [ ] **Step 5: Run the whole quiz suite + lint**

```
go test ./pkg/quiz/... -v
go vet ./pkg/quiz/...
gofmt -l pkg/quiz/
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add pkg/quiz/results.go pkg/quiz/results_test.go
git commit -m "$(cat <<'EOF'
Spec D: GetAttempt + History

[+] AttemptView with per-question review (stem, options, userAnswer, correctAnswer, explanation, correct)
[+] History lists every attempt on a quiz by the current user
EOF
)"
```

---

## Phase 4 — HTTP handlers, routing, OpenAPI

The service layer is complete. Phase 4 replaces `api/handler/quiz_stub.go` with real endpoints and re-paths the routes to match Spec D §3 (`/quizzes/*`, not `/quiz/*`).

### Task 16: Replace `quiz_stub.go` with real handler

**Files:**
- Modify: `api/handler/quiz_stub.go` → `api/handler/quiz.go` (rename) — actual filename is up to the executor; in this plan we keep `quiz_stub.go` and rewrite its contents (simpler git history). Either is fine.
- Create: `api/handler/quiz_test.go` (per-endpoint tests follow in Tasks 17-19)

- [ ] **Step 1: Rewrite the handler skeleton**

Replace `api/handler/quiz_stub.go`:

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/middleware"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/quiz"
)

// QuizHandler exposes the Spec D quiz endpoints.
type QuizHandler struct {
	svc    *quiz.Service   // svc owns all quiz domain operations
	access *access.Service // access answers the entitlement gate
}

// NewQuizHandler constructs a QuizHandler.
func NewQuizHandler(svc *quiz.Service, acc *access.Service) *QuizHandler {
	return &QuizHandler{svc: svc, access: acc}
}

// requireAIAccess is the entitlement check shared by generation endpoints.
// Plan D3 will extend this with the quizDemoUsed demo path.
func (h *QuizHandler) requireAIAccess(ctx context.Context, uid int64) error {
	ok, err := h.access.HasAIAccess(ctx, uid)
	if err != nil {
		return err
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// quizIDFromPath parses the {id} path value from r.URL.Path.
// Handlers registered with stdlib mux's `POST /quizzes/{id}/...` shape can read it via r.PathValue.
func quizIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}

func attemptIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("aid")
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
	}
	return id, nil
}
```

Make sure to update the import list (`context`, `fmt`, etc. as needed). If `access.Service` is not yet on `deps`, locate where `d.access` is initialized in `cmd/app/deps.go` — it already exists (see `pkg/access/service.go:44`).

- [ ] **Step 2: Wire the new constructor**

In `cmd/app/routes.go` (line 165): change

```go
quizH := handler.NewQuizHandler(d.quiz)
```

to

```go
quizH := handler.NewQuizHandler(d.quiz, d.access)
```

- [ ] **Step 3: Build to verify**

```
go build ./...
```

Expected: clean. (The Generate/Attempt/Share methods are gone; the route registration in Step 4 of Task 21 will replace those bindings before this can run.)

- [ ] **Step 4: Commit**

```bash
git add api/handler/quiz_stub.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec D: quiz handler skeleton + access dependency

[&] api/handler/quiz_stub.go: replace stub methods with handler skeleton
[&] cmd/app/routes.go: pass access.Service into NewQuizHandler
EOF
)"
```

### Task 17: `POST /quizzes/generate` handler

**Files:**
- Modify: `api/handler/quiz_stub.go` (add `Generate` handler)
- Test: `api/handler/quiz_generate_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestPostQuizzesGenerate_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")
	c1 := testutil.NewChapter(t, pool, sid, "C1")
	testutil.NewFlashcard(t, pool, c1, "Mito", "What?", "Mitochondrion")

	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"questionType":"multi_choice","stem":"What?","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[1]}]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, testutil.AccessSvc(t, pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")
	qsvc := quiz.NewService(pool, ai)
	h := handler.NewQuizHandler(qsvc, testutil.AccessSvc(t, pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId":  sid,
		"kind":       "specific",
		"size":       1,
		"types":      []string{"multi_choice"},
		"cardFilter": "all",
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Generate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out struct {
		QuizID        int64 `json:"quizId"`
		QuestionCount int   `json:"questionCount"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.QuizID == 0 || out.QuestionCount != 1 {
		t.Fatalf("response = %+v", out)
	}
}

func TestPostQuizzesGenerate_NoAIAccess_402(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, testutil.AccessSvc(t, pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId": sid, "kind": "global", "size": 5, "types": []string{"multi_choice"},
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Generate(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
	}
}
```

`testutil.WithAuthedUser` is the existing test helper that injects the auth context value; verify the name with `grep -n "func WithAuthedUser\|func InjectUser\|UserID context" testutil/` and use the actual helper. Same for `testutil.AccessSvc`.

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL — `Generate` method gone after Task 16's rewrite.

- [ ] **Step 3: Add the `Generate` handler**

Append to `api/handler/quiz_stub.go`:

```go
// generateRequest is the JSON shape for POST /quizzes/generate.
type generateRequest struct {
	SubjectID  int64    `json:"subjectId"`
	ChapterID  *int64   `json:"chapterId,omitempty"`
	Kind       string   `json:"kind"`
	Size       int      `json:"size"`
	Types      []string `json:"types"`
	CardFilter string   `json:"cardFilter,omitempty"`
	// PlanContext is added in Plan D2.
}

// Generate handles POST /quizzes/generate.
func (h *QuizHandler) Generate(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.requireAIAccess(r.Context(), uid); err != nil {
		httpx.WriteError(w, err)
		return
	}

	var body generateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err))
		return
	}

	types := make([]quiz.QuestionType, 0, len(body.Types))
	for _, t := range body.Types {
		types = append(types, quiz.QuestionType(t))
	}
	req := quiz.GenerateRequest{
		UserID:     uid,
		SubjectID:  body.SubjectID,
		ChapterID:  body.ChapterID,
		Kind:       quiz.Kind(body.Kind),
		Size:       body.Size,
		Types:      types,
		CardFilter: quiz.CardFilter(body.CardFilter),
	}
	res, err := h.svc.Generate(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"quizId":        res.QuizID,
		"questionCount": res.QuestionCount,
		"kind":          res.Kind,
	})
}
```

(If `httpx.WriteJSON` doesn't exist with that signature, grep `internal/httpx/` for the response helper and adapt — every other handler in the repo uses the same idiom.)

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestPostQuizzesGenerate' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go api/handler/quiz_generate_test.go
git commit -m "$(cat <<'EOF'
Spec D: POST /quizzes/generate handler

[+] generateRequest body shape; entitlement check via access service
[+] handler test: happy path, 402 on no AI access
EOF
)"
```

### Task 18: `POST /quizzes/:id/start` + `GET /quizzes/:id/attempts/:aid/resume`

**Files:**
- Modify: `api/handler/quiz_stub.go`
- Test: `api/handler/quiz_play_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestPostQuizzesStart_CreatesAttemptAndReturnsFirstQuestion(t *testing.T) {
	// ... setup user + quiz with 2 questions ...
	req := httptest.NewRequest("POST", "/quizzes/{id}/start", nil)
	req.SetPathValue("id", fmt.Sprintf("%d", qid))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Start(w, req)
	if w.Code != 200 { t.Fatalf("status %d", w.Code) }
	var resp struct {
		AttemptID    int64 `json:"attemptId"`
		NextQuestion struct {
			ID      int64  `json:"id"`
			Ordinal int    `json:"ordinal"`
			Type    string `json:"type"`
		} `json:"nextQuestion"`
		Progress struct{ Answered, Total int } `json:"progress"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.NextQuestion.Ordinal != 1 { t.Fatalf("nextQuestion.Ordinal = %d", resp.NextQuestion.Ordinal) }
	if resp.Progress.Total != 2 { t.Fatalf("Total = %d, want 2", resp.Progress.Total) }
}

func TestGetResume_ReturnsCurrentPosition(t *testing.T) {
	// ... start an attempt, then call Resume ...
	req := httptest.NewRequest("GET", "/quizzes/{id}/attempts/{aid}/resume", nil)
	req.SetPathValue("id", fmt.Sprintf("%d", qid))
	req.SetPathValue("aid", fmt.Sprintf("%d", attemptID))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Resume(w, req)
	if w.Code != 200 { t.Fatalf("status %d", w.Code) }
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement handlers**

Append to `api/handler/quiz_stub.go`:

```go
// Start handles POST /quizzes/{id}/start.
func (h *QuizHandler) Start(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	att, next, prog, err := h.svc.Start(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"attemptId":    att.ID,
		"nextQuestion": next,
		"progress":     prog,
	})
}

// Resume handles GET /quizzes/{id}/attempts/{aid}/resume.
func (h *QuizHandler) Resume(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	attemptID, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	att, err := h.svc.LoadAttemptForUser(r.Context(), uid, attemptID)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	next, prog, err := h.svc.AdvanceForUser(r.Context(), uid, attemptID)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"attemptId":    att.ID,
		"state":        att.State,
		"nextQuestion": next,
		"progress":     prog,
	})
}
```

`LoadAttemptForUser` and `AdvanceForUser` are thin wrappers over `loadAttempt` + ownership check and `advance`. Add them at the bottom of `pkg/quiz/attempt.go`:

```go
// LoadAttemptForUser is the public projection of loadAttempt with an ownership check.
func (s *Service) LoadAttemptForUser(ctx context.Context, uid, attemptID int64) (Attempt, error) {
	att, err := s.loadAttempt(ctx, attemptID)
	if err != nil {
		return att, err
	}
	if att.UserID != uid {
		return att, myErrors.ErrForbidden
	}
	return att, nil
}

// AdvanceForUser returns the next-question + progress payload after an ownership check.
func (s *Service) AdvanceForUser(ctx context.Context, uid, attemptID int64) (*PublicQuestion, Progress, error) {
	if _, err := s.LoadAttemptForUser(ctx, uid, attemptID); err != nil {
		return nil, Progress{}, err
	}
	return s.advance(ctx, attemptID)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestPostQuizzesStart|TestGetResume' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go api/handler/quiz_play_test.go pkg/quiz/attempt.go
git commit -m "$(cat <<'EOF'
Spec D: Start + Resume handlers

[+] POST /quizzes/{id}/start: idempotent, returns first/next question
[+] GET  /quizzes/{id}/attempts/{aid}/resume: position pickup
[+] LoadAttemptForUser, AdvanceForUser ownership-checked wrappers
EOF
)"
```

### Task 19: `POST /quizzes/:id/attempts/:aid/answer` + `/abandon` + `/retake`

**Files:**
- Modify: `api/handler/quiz_stub.go`
- Test: `api/handler/quiz_play_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestPostAnswer_CorrectMCQ(t *testing.T) {
	// ... start an attempt, get q1.ID ...
	body, _ := json.Marshal(map[string]any{
		"questionId": q1ID,
		"answer":     map[string]int{"index": 2},
	})
	req := httptest.NewRequest("POST", "/quizzes/{id}/attempts/{aid}/answer", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(quizID, 10))
	req.SetPathValue("aid", strconv.FormatInt(attemptID, 10))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Answer(w, req)
	if w.Code != 200 { t.Fatalf("status %d body=%s", w.Code, w.Body.String()) }

	var resp struct {
		Correct bool `json:"correct"`
		Next    *struct{ Ordinal int } `json:"nextQuestion"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Correct { t.Fatalf("not marked correct") }
}

func TestPostAbandon_204(t *testing.T) {
	// ... start an attempt ...
	req := httptest.NewRequest("POST", "/quizzes/{id}/attempts/{aid}/abandon", nil)
	req.SetPathValue("id", strconv.FormatInt(quizID, 10))
	req.SetPathValue("aid", strconv.FormatInt(attemptID, 10))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Abandon(w, req)
	if w.Code != http.StatusNoContent { t.Fatalf("status %d", w.Code) }
}

func TestPostRetake_409IfInProgress(t *testing.T) {
	// ... start an attempt, leave it open, then call retake ...
	req := httptest.NewRequest("POST", "/quizzes/{id}/retake", nil)
	req.SetPathValue("id", strconv.FormatInt(quizID, 10))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.Retake(w, req)
	if w.Code != http.StatusConflict { t.Fatalf("status %d", w.Code) }
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement handlers**

Append to `api/handler/quiz_stub.go`:

```go
// answerRequest is the JSON shape for POST /quizzes/{id}/attempts/{aid}/answer.
type answerRequest struct {
	QuestionID int64           `json:"questionId"`
	Answer     json.RawMessage `json:"answer"`
}

// Answer handles POST /quizzes/{id}/attempts/{aid}/answer.
func (h *QuizHandler) Answer(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	var body answerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)); return
	}
	res, err := h.svc.Answer(r.Context(), uid, aid, body.QuestionID, body.Answer)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Abandon handles POST /quizzes/{id}/attempts/{aid}/abandon.
func (h *QuizHandler) Abandon(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	if err := h.svc.Abandon(r.Context(), uid, aid); err != nil {
		httpx.WriteError(w, err); return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Retake handles POST /quizzes/{id}/retake.
func (h *QuizHandler) Retake(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	att, err := h.svc.Retake(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attemptId": att.ID})
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestPostAnswer|TestPostAbandon|TestPostRetake' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go api/handler/quiz_play_test.go
git commit -m "$(cat <<'EOF'
Spec D: Answer + Abandon + Retake handlers

[+] POST /quizzes/{id}/attempts/{aid}/answer
[+] POST /quizzes/{id}/attempts/{aid}/abandon (204)
[+] POST /quizzes/{id}/retake (409 if in-progress)
EOF
)"
```

### Task 20: `GET /quizzes/:id/attempts/:aid` + `GET /quizzes/:id/history`

**Files:**
- Modify: `api/handler/quiz_stub.go`
- Test: `api/handler/quiz_play_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestGetAttempt_ReturnsReviewPayload(t *testing.T) {
	// ... complete an attempt, then GET it ...
	req := httptest.NewRequest("GET", "/quizzes/{id}/attempts/{aid}", nil)
	req.SetPathValue("id", strconv.FormatInt(quizID, 10))
	req.SetPathValue("aid", strconv.FormatInt(attemptID, 10))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.GetAttempt(w, req)
	if w.Code != 200 { t.Fatalf("status %d", w.Code) }

	var view struct {
		Attempt   struct{ State string `json:"state"` } `json:"attempt"`
		Questions []struct{ Stem string `json:"stem"` } `json:"questions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if view.Attempt.State != "completed" { t.Fatalf("state = %q", view.Attempt.State) }
	if len(view.Questions) == 0 { t.Fatalf("empty questions") }
}

func TestGetHistory_ReturnsAttempts(t *testing.T) {
	// ... create 2 attempts ...
	req := httptest.NewRequest("GET", "/quizzes/{id}/history", nil)
	req.SetPathValue("id", strconv.FormatInt(quizID, 10))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h.History(w, req)
	if w.Code != 200 { t.Fatalf("status %d", w.Code) }
	var resp struct{ Attempts []struct{ ID int64 } `json:"attempts"` }
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Attempts) != 2 { t.Fatalf("got %d, want 2", len(resp.Attempts)) }
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement handlers**

Append to `api/handler/quiz_stub.go`:

```go
// GetAttempt handles GET /quizzes/{id}/attempts/{aid}.
func (h *QuizHandler) GetAttempt(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	aid, err := attemptIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	view, err := h.svc.GetAttempt(r.Context(), uid, aid)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// History handles GET /quizzes/{id}/history.
func (h *QuizHandler) History(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	atts, err := h.svc.History(r.Context(), uid, qid)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attempts": atts})
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestGetAttempt|TestGetHistory' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go api/handler/quiz_play_test.go
git commit -m "$(cat <<'EOF'
Spec D: GetAttempt + History handlers

[+] GET /quizzes/{id}/attempts/{aid}
[+] GET /quizzes/{id}/history
EOF
)"
```

### Task 21: Route wiring + OpenAPI

Move the quiz routes out of `registerStubRoutes` into `registerVerifiedRoutes` (the play loop requires email verification anyway). Update the paths from `/quiz/*` to `/quizzes/*` and `/quizzes/{id}/...`. Add the OpenAPI documentation.

**Files:**
- Modify: `cmd/app/routes.go`
- Modify: `api/handler/docs_openapi.yaml`

- [ ] **Step 1: Update `cmd/app/routes.go`**

Remove the three lines in `registerStubRoutes` (162-178 region):

```go
mux.Handle("POST /quiz/generate", av(quizH.Generate))
mux.Handle("POST /quiz/attempt", av(quizH.Attempt))
mux.Handle("POST /quiz/share", av(quizH.Share))
```

Add to `registerVerifiedRoutes` (near the plan endpoints at the bottom):

```go
quizH := handler.NewQuizHandler(d.quiz, d.access)
mux.Handle("POST /quizzes/generate",                                 av(quizH.Generate))
mux.Handle("POST /quizzes/{id}/start",                                av(quizH.Start))
mux.Handle("GET  /quizzes/{id}/attempts/{aid}/resume",                av(quizH.Resume))
mux.Handle("POST /quizzes/{id}/attempts/{aid}/answer",                av(quizH.Answer))
mux.Handle("POST /quizzes/{id}/attempts/{aid}/abandon",               av(quizH.Abandon))
mux.Handle("POST /quizzes/{id}/retake",                                av(quizH.Retake))
mux.Handle("GET  /quizzes/{id}/attempts/{aid}",                       av(quizH.GetAttempt))
mux.Handle("GET  /quizzes/{id}/history",                              av(quizH.History))
```

(Note the spacing in the strings is purely for readability — `mux.Handle` ignores it. Drop the column alignment if the codebase doesn't use it elsewhere.)

Also remove the duplicate `quizH := handler.NewQuizHandler(...)` line from `registerStubRoutes`.

- [ ] **Step 2: Verify the binary still builds and routes pass smoke**

```
go build ./...
go run ./cmd/app &
# wait 1s
curl -s -o /dev/null -w "%{http_code}\n" -X POST -H "Authorization: Bearer <test-jwt>" http://localhost:8080/quizzes/generate -d '{}'
# Expected: 401 or 400 (auth/parse error) — what matters is the route exists and isn't 404.
kill %1
```

(Substitute a real test JWT, or just hit `OPTIONS /quizzes/generate` if CORS is set up.)

- [ ] **Step 3: Update OpenAPI**

Edit `api/handler/docs_openapi.yaml`. Remove the line that says "Stub routes (quiz, plan, duel) are omitted" (line 7 area) — at least the quiz portion. Add the quiz endpoints under `paths:`. Reuse existing schemas where possible.

```yaml
  /quizzes/generate:
    post:
      tags: [Quiz]
      summary: Generate a new quiz
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [subjectId, kind, size, types]
              properties:
                subjectId:  { type: integer, format: int64 }
                chapterId:  { type: integer, format: int64, nullable: true }
                kind:       { type: string, enum: [specific, global] }
                size:       { type: integer, enum: [5, 10, 15, 20] }
                types:
                  type: array
                  items: { type: string, enum: [multi_choice, true_false, fill_blank] }
                cardFilter: { type: string, enum: [all, bad_ok, due] }
      responses:
        '200':
          description: Quiz created
          content:
            application/json:
              schema:
                type: object
                properties:
                  quizId:        { type: integer, format: int64 }
                  questionCount: { type: integer }
                  kind:          { type: string }
        '402': { $ref: '#/components/responses/PaymentRequired' }
        '429': { $ref: '#/components/responses/TooManyRequests' }

  /quizzes/{id}/start:
    post:
      tags: [Quiz]
      summary: Start (or resume) an attempt
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '200':
          description: Attempt + first/next question
          content:
            application/json:
              schema:
                type: object
                properties:
                  attemptId: { type: integer, format: int64 }
                  nextQuestion: { $ref: '#/components/schemas/PublicQuestion' }
                  progress:     { $ref: '#/components/schemas/QuizProgress' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }

  /quizzes/{id}/attempts/{aid}/resume:
    get:
      tags: [Quiz]
      summary: Resume an in-progress attempt
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
        - in: path
          name: aid
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '200':
          description: Current position
          content:
            application/json:
              schema:
                type: object
                properties:
                  attemptId:    { type: integer, format: int64 }
                  state:        { type: string }
                  nextQuestion: { $ref: '#/components/schemas/PublicQuestion' }
                  progress:     { $ref: '#/components/schemas/QuizProgress' }

  /quizzes/{id}/attempts/{aid}/answer:
    post:
      tags: [Quiz]
      summary: Submit one answer
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
        - in: path
          name: aid
          required: true
          schema: { type: integer, format: int64 }
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [questionId, answer]
              properties:
                questionId: { type: integer, format: int64 }
                answer:
                  oneOf:
                    - { type: object, properties: { index: { type: integer } }, required: [index] }
                    - { type: object, properties: { value: { type: boolean } }, required: [value] }
                    - { type: object, properties: { value: { type: string  } }, required: [value] }
      responses:
        '200':
          description: Scored
          content:
            application/json:
              schema:
                type: object
                properties:
                  correct:       { type: boolean }
                  correctAnswer: {}
                  explanation:   { type: string }
                  nextQuestion:  { $ref: '#/components/schemas/PublicQuestion', nullable: true }

  /quizzes/{id}/attempts/{aid}/abandon:
    post:
      tags: [Quiz]
      summary: Abandon an in-progress attempt
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
        - in: path
          name: aid
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '204':
          description: Abandoned

  /quizzes/{id}/retake:
    post:
      tags: [Quiz]
      summary: Start a new attempt over the same questions
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '200':
          description: New attempt id
          content:
            application/json:
              schema:
                type: object
                properties:
                  attemptId: { type: integer, format: int64 }
        '409': { $ref: '#/components/responses/Conflict' }

  /quizzes/{id}/attempts/{aid}:
    get:
      tags: [Quiz]
      summary: Full review payload
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
        - in: path
          name: aid
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '200':
          description: AttemptView
          content:
            application/json:
              schema: { $ref: '#/components/schemas/AttemptView' }

  /quizzes/{id}/history:
    get:
      tags: [Quiz]
      summary: All attempts for this quiz by the current user
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '200':
          description: Attempts
          content:
            application/json:
              schema:
                type: object
                properties:
                  attempts:
                    type: array
                    items: { $ref: '#/components/schemas/QuizAttempt' }
```

Under `components.schemas:`, add `PublicQuestion`, `QuizProgress`, `QuizAttempt`, `AttemptView`. Mirror the shape produced by the handler/service layer. (If `Forbidden`, `NotFound`, `Conflict`, `PaymentRequired`, `TooManyRequests` aren't already under `components.responses`, add the standard error schema for them.)

- [ ] **Step 4: Smoke the OpenAPI**

```
go test ./api/handler/ -run 'TestDocs|TestOpenAPI' -v
```

Expected: PASS (the existing docs test parses the YAML). If there is no parse test, run `python3 -c "import yaml,sys; yaml.safe_load(open('api/handler/docs_openapi.yaml'))"` as a quick syntax check.

- [ ] **Step 5: Run the full suite + lint**

```
make test
go vet ./...
gofmt -l .
```

Expected: PASS, clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/app/routes.go api/handler/docs_openapi.yaml
git commit -m "$(cat <<'EOF'
Spec D: wire quiz routes + OpenAPI

[&] cmd/app/routes.go: /quiz/* stubs replaced with /quizzes/* real routes
[+] OpenAPI: 8 quiz endpoints + PublicQuestion/QuizProgress/QuizAttempt/AttemptView schemas
EOF
)"
```

---

## Self-review checklist

After all tasks complete, run this checklist:

1. **Spec coverage** — verify each clause of Spec D §1-3 (excluding plan/sharing) maps to a task:
   - §2 schema (quizzes / quiz_questions / quiz_attempts / quiz_attempt_answers): Tasks 1-2.
   - §2 immutability + partial UNIQUE: Tasks 2 + 12-13.
   - §3 generation endpoint: Task 17.
   - §3 play (start/resume/answer/abandon): Tasks 18-19.
   - §3 retake: Task 19.
   - §3 results + history: Task 20.
   - §5.1-5.2 flow: Tasks 11-13.
   - §5.4 retake (no AI call): Task 14.
   - §5.7 idempotency (PK ON CONFLICT, partial UNIQUE): Tasks 2 + 13.
   - §1 architecture: "all AI calls go through RunStructuredGeneration" — Tasks 4-7 enforce this.
   - §1 "correctness never sent before submission" — handler returns `PublicQuestion` (no `CorrectJSON`); Task 8 + assertions in 13.
2. **Cross-spec deferrals** — verified Plan D2 covers plan integration (PlanContext branch in Generate, plan-progress writeback) and Plan D3 covers sharing + quality + demo. No stubbed concerns leak into D1.
3. **Type consistency** — `Quiz.Source` enum matches `Source` constants; `quiz.QuestionType` matches the SQL CHECK; field names round-trip through `Attempt`/`PublicQuestion`/`AttemptView`.
4. **No placeholders** — every step shows the code; SQL is concrete; commit messages don't say "TODO".
5. **CLAUDE.md fit** — function sizes look OK (most under 30 lines); files under 300 lines projected; commit format matches.

If anything fails, fix inline.

---

## Execution handoff

When implementation is complete:

- Run `make test` clean.
- Run `go vet ./...` clean.
- Verify the `feat/spec-d-ai-quiz` branch shows ~21 commits matching the task headings.
- Open a PR against `master` titled `Spec D Part 1: quiz core (generate/play/retake)` with a body that summarises the new endpoints + the schema reconciliation, and links to `docs/superpowers/specs/2026-04-21-ai-quiz-design.md`.
- Do **not** merge until Plans D2 and D3 are ready — or carve this PR as its own merge unit if the team prefers smaller PRs (the spec is designed for it: `source='user'` quizzes are usable end-to-end on their own).
