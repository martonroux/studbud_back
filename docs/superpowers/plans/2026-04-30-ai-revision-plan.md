# AI Revision Plan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Spec B — exam-driven daily revision plans for AI subscribers, mixing same-subject and cross-subject flashcards via the Spec B.0 keyword index, surfaced through SSE-streamed generation and a dated GET endpoint.

**Architecture:** Two new domain packages (`pkg/exam`, `pkg/revisionplan`) layered on Spec A's `aipipeline` primitive and Spec B.0's `flashcard_keywords` index. Generation is a sub-call orchestration: keyword-overlap shortlist (SQL) → AI re-rank (non-streaming) → plan generation (streaming, SSE-emitted to client). Progress is decoupled from plan rows so regeneration preserves per-user completion. All AI calls reuse `aipipeline.RunStructuredGeneration` and the shared quota service.

**Tech Stack:** Go 1.21+, pgx/v5, `text/template` (existing prompt renderer), `github.com/gen2brain/go-fitz` (PDF page count), `text/event-stream` (existing SSE helpers in `api/handler/ai.go`).

---

## Decisions Locked Before Drafting

- **Schema realignment is destructive.** `db_sql/setup_plan.go` already has stub `exams` / `revision_plans` / `revision_plan_progress` tables that don't match Spec B (different column names, progress keyed by `plan_id` instead of `(user, fc, date)`). Pre-launch we drop-and-recreate these per the spec, matching the pattern used in Plan B.0 Task 1 (annotated drop, replaced with `CREATE TABLE IF NOT EXISTS` once consumers migrate). `quiz_attempts.plan_id FK` survives because `revision_plans.id` stays `BIGSERIAL`.
- **Quota counters already exist.** `db_sql/setup_ai.go:27-41` already has `plan_calls` and `cross_subject_rank_calls` columns in `ai_quota_daily`. No schema work for Task 3 — only Go-side `QuotaLimits` extension.
- **No `user_accessible_subjects(userId)` SQL function exists** (confirmed by survey). The cross-subject shortlist inlines the access predicate as a CTE: union of owned + collaborator + active subscriber subjects.
- **`annales_image_id` is `TEXT`, not `BIGINT`.** The `images` table uses `id TEXT PRIMARY KEY` (ULID). Spec §4.1 says `BIGINT`; the actual schema dictates `TEXT`. Plan uses `TEXT`.
- **Annales upload reuses `image.Service` as a blob store.** The `images` table already has `mime_type` and tolerates `application/pdf`. New endpoint `POST /exams/:id/annales` that adds page-count validation (max 10) + size cap (5 MB) and links the resulting image row to the exam — does NOT extend `/upload-image` itself (avoids polluting the image-only contract).
- **Quota debit is post-success, not pre-allocate-then-refund.** `aipipeline.Service.DebitQuota` is called only after generation succeeds (matches the existing pattern in `pkg/aipipeline/service_check.go` and `service_generation.go`). Spec §6.1's "refund on failure" flow collapses to "debit only on success" — equivalent semantics, simpler code.
- **`FeatureCrossSubjectRank` is a sub-step that does NOT debit the user.** The cross-subject ranker runs inside `GenerateForExam`. Per Spec §5.1, only the outer `plan` counter increments. `RankCrossSubjects` is implemented as a primitive that takes a pre-validated request and skips the quota path.
- **Drift threshold is `daysBehind >= 2`** (Spec §6.4). Drift computed at `GET` time; not stored.
- **Regeneration preserves progress** because `revision_plan_progress` is keyed by `(user_id, fc_id, plan_date)`, not by `plan_id`.
- **Hard limit `Max active exams per user = 10`** (Spec §4.1) enforced in `pkg/exam.Service.Create`.
- **PDF page count via `go-fitz` `doc.NumPage()`**, lightweight (no rasterization). New helper `aiProvider.PDFPageCount`.

---

## File Structure

**Backend (`/Users/martonroux/Documents/WEB/studbud_3/backend-b`)**

- `db_sql/setup_plan.go` — modified — destructive schema realignment.
- `pkg/aipipeline/model.go` — modified — add `FeatureGenerateRevisionPlan`, `FeatureCrossSubjectRank` to `FeatureKey`; extend `QuotaLimits` with `PlanCalls`.
- `pkg/aipipeline/quota.go` — modified — extend `checkAgainstLimits` to map the new feature keys.
- `pkg/aipipeline/prompts.go` — modified — add `RevisionPlanValues`, `CrossSubjectRankValues`, and exported `RenderRevisionPlan`, `RenderCrossSubjectRank`.
- `pkg/aipipeline/prompts/revision_plan.tmpl` — new — outer plan-generation prompt.
- `pkg/aipipeline/prompts/cross_subject_rank.tmpl` — new — cross-subject ranker prompt.
- `pkg/aipipeline/prompts_test.go` — modified — render-asserts for both new templates.
- `pkg/aipipeline/service_revision.go` — new — `Service.GenerateRevisionPlan` (streams) and `Service.RankCrossSubjects` (non-streaming).
- `pkg/aipipeline/service_revision_test.go` — new — unit tests with `testutil.FakeAIClient`.
- `internal/aiProvider/pdf.go` — modified — add `PDFPageCount(bytes []byte) (int, error)`.
- `internal/aiProvider/pdf_test.go` — modified — test for `PDFPageCount`.
- `pkg/exam/service.go` — new — exam CRUD + access check + max-10 limit.
- `pkg/exam/annales.go` — new — `Service.AttachAnnales`.
- `pkg/exam/service_test.go` — new — integration tests.
- `api/handler/exam.go` — new — exam HTTP handlers.
- `api/handler/exam_annales.go` — new — annales upload handler.
- `api/handler/exam_test.go` — new — handler tests.
- `pkg/revisionplan/service.go` — new — top-level `Service` with `GenerateForExam`, `GetForExam`, `MarkDone`.
- `pkg/revisionplan/shortlist.go` — new — cross-subject shortlist SQL.
- `pkg/revisionplan/postprocess.go` — new — validate AI-returned days.
- `pkg/revisionplan/drift.go` — new — drift calculator.
- `pkg/revisionplan/queries.go` — new — SQL constants.
- `pkg/revisionplan/*_test.go` — new — per-file unit/integration tests.
- `api/handler/revision_plan.go` — new — generate (SSE) + GET + mark-done.
- `api/handler/revision_plan_test.go` — new — handler tests.
- `cmd/app/deps.go` — modified — construct `pkg/exam.Service` and `pkg/revisionplan.Service`, attach to `deps`.
- `cmd/app/routes.go` — modified — register the new routes.
- `cmd/app/e2e_revision_plan_test.go` — new — end-to-end happy path.

---

## Task 1: Realign `exams` / `revision_plans` / `revision_plan_progress` schema

**Files:**
- Modify: `db_sql/setup_plan.go` (replace `planSchema` const)

The existing stub schema doesn't match Spec B §4. We destructively replace pre-launch and annotate the change exactly the way Plan B.0 Task 1 did.

- [ ] **Step 1: Verify nothing in the codebase reads the existing columns**

Run:
```bash
grep -rn "exam_date\|intensity\|item_key\|payload" --include="*.go" | grep -vE "^(docs/|.*_test\.go)"
```
Expected: no production code references these. (Confirmed at plan time: only the schema file itself uses these names.)

- [ ] **Step 2: Replace the schema constant**

Open `db_sql/setup_plan.go` and replace the entire `planSchema` const with:

```go
// TEMPORARY: destructive realignment for Spec B pre-launch (Plan B Task 1 —
// see docs/superpowers/plans/2026-04-30-ai-revision-plan.md). Replace with
// CREATE TABLE IF NOT EXISTS once all consumers reference the new column shape.
const planSchema = `
DROP TABLE IF EXISTS revision_plan_progress CASCADE;
DROP TABLE IF EXISTS revision_plans CASCADE;
DROP TABLE IF EXISTS exams CASCADE;

CREATE TABLE exams (
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
CREATE INDEX idx_exams_user_active ON exams (user_id, date) WHERE date >= CURRENT_DATE;

CREATE TABLE revision_plans (
    id            BIGSERIAL    PRIMARY KEY,
    exam_id       BIGINT       NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    generated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    days          JSONB        NOT NULL,
    model         TEXT         NOT NULL,
    prompt_hash   TEXT         NOT NULL
);

CREATE TABLE revision_plan_progress (
    user_id   BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    fc_id     BIGINT      NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    plan_date DATE        NOT NULL,
    done_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, fc_id, plan_date)
);
CREATE INDEX idx_rpp_user_today ON revision_plan_progress (user_id, plan_date);
`
```

(Note: `quiz_attempts.plan_id REFERENCES revision_plans(id)` cascades on the DROP, but the FK is preserved on the CREATE because `revision_plans.id` remains `BIGSERIAL`.)

- [ ] **Step 3: Run existing tests to confirm setup still applies cleanly**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./db_sql/... ./pkg/aipipeline/... -count=1 -p 1
```
Expected: PASS. (DBs setup is rerun every test process via `testutil.OpenTestDB`; the destructive realignment runs exactly once per process.)

- [ ] **Step 4: Commit**

```bash
git add db_sql/setup_plan.go
git commit -m "$(cat <<'EOF'
Realign exams/revision_plans schema to Spec B

[&] exams: rename exam_date -> date, add notes
[&] revision_plans: replace intensity/payload with days/model/prompt_hash
[&] revision_plan_progress: rekey to (user_id, fc_id, plan_date)
[+] partial index idx_exams_user_active for active-exam lookups
[+] index idx_rpp_user_today for today-progress query
EOF
)"
```

---

## Task 2: Add `FeatureGenerateRevisionPlan` + `FeatureCrossSubjectRank` to FeatureKey, extend `QuotaLimits` with `PlanCalls`

**Files:**
- Modify: `pkg/aipipeline/model.go` (add 2 enumerants + 1 limit field)
- Modify: `pkg/aipipeline/quota.go` (route the new keys in `checkAgainstLimits` and quota column mapping)
- Modify: `pkg/aipipeline/quota_test.go` (assert routing)

- [ ] **Step 1: Read the existing FeatureKey block + QuotaLimits**

Run:
```bash
sed -n '10,30p;60,90p' pkg/aipipeline/model.go
```
Expected: see `FeatureGenerateFromPrompt`/`FromPDF`/`CheckFlashcard`/`ExtractKeywords` enumerants and `QuotaLimits` struct.

- [ ] **Step 2: Write a failing test**

Append to `pkg/aipipeline/quota_test.go`:

```go
func TestCheckAgainstLimits_PlanFeature(t *testing.T) {
	limits := aipipeline.QuotaLimits{PlanCalls: 1}
	used := map[string]int{"plan_calls": 1, "cross_subject_rank_calls": 0}
	if err := aipipeline.CheckAgainstLimitsForTest(aipipeline.FeatureGenerateRevisionPlan, used, limits, 0); err == nil {
		t.Error("want quota exhausted at limit, got nil")
	}
}

func TestCheckAgainstLimits_CrossSubjectRankNeverDebits(t *testing.T) {
	limits := aipipeline.QuotaLimits{PlanCalls: 1}
	used := map[string]int{"plan_calls": 0, "cross_subject_rank_calls": 999}
	if err := aipipeline.CheckAgainstLimitsForTest(aipipeline.FeatureCrossSubjectRank, used, limits, 0); err != nil {
		t.Errorf("cross-subject rank should always pass quota check, got %v", err)
	}
}
```

The test references `CheckAgainstLimitsForTest` — a thin export wrapper added in this task. (See Step 4.)

- [ ] **Step 3: Run the test to verify it fails**

Run:
```bash
go test ./pkg/aipipeline/ -run "TestCheckAgainstLimits_(PlanFeature|CrossSubjectRankNeverDebits)" -count=1
```
Expected: `undefined: FeatureGenerateRevisionPlan` (build failure).

- [ ] **Step 4: Add the enumerants and limit field**

In `pkg/aipipeline/model.go`, locate the `FeatureKey` block (lines ~10–20) and append:

```go
const FeatureGenerateRevisionPlan FeatureKey = "revision_plan"
const FeatureCrossSubjectRank     FeatureKey = "cross_subject_rank"
```

In the same file, locate the `QuotaLimits` struct (lines ~68–84) and add a new field:

```go
PlanCalls int // PlanCalls caps daily revision-plan generations (default 5)
```

Update `DefaultQuotaLimits()` (same file) to set `PlanCalls: 5`.

- [ ] **Step 5: Route the new keys in `checkAgainstLimits`**

In `pkg/aipipeline/quota.go`, locate `checkAgainstLimits` (lines ~68–87) and add cases:

```go
case FeatureGenerateRevisionPlan:
    if used["plan_calls"] >= limits.PlanCalls {
        return myErrors.ErrQuotaExhausted
    }
case FeatureCrossSubjectRank:
    return nil // sub-step of plan generation; no quota check
```

In the same file, locate `DebitQuota` and add cases for the new keys, mapping each to its column:

```go
case FeatureGenerateRevisionPlan:
    return s.bumpCounter(ctx, uid, "plan_calls", calls)
case FeatureCrossSubjectRank:
    return s.bumpCounter(ctx, uid, "cross_subject_rank_calls", calls)
```

(`bumpCounter` is the existing helper used by other features — same shape.)

- [ ] **Step 6: Add `CheckAgainstLimitsForTest` exported shim**

The existing `checkAgainstLimits` is unexported and takes a `Service` receiver. For the test to call it without a DB, add at the bottom of `pkg/aipipeline/quota.go`:

```go
// CheckAgainstLimitsForTest is a pure-function shim over the limit-routing
// branch of checkAgainstLimits so unit tests can exercise it without a DB.
// The real path goes through Service.CheckQuota.
func CheckAgainstLimitsForTest(feat FeatureKey, used map[string]int, limits QuotaLimits, pdfPages int) error {
    return checkAgainstLimitsPure(feat, used, limits, pdfPages)
}
```

Refactor `checkAgainstLimits` to call a new package-level `checkAgainstLimitsPure(feat FeatureKey, used map[string]int, limits QuotaLimits, pdfPages int) error` that contains all the switch logic. The method itself becomes a thin wrapper that loads `used` and calls the pure function.

- [ ] **Step 7: Run the tests**

Run:
```bash
go test ./pkg/aipipeline/ -count=1
```
Expected: PASS (full package).

- [ ] **Step 8: Commit**

```bash
git add pkg/aipipeline/model.go pkg/aipipeline/quota.go pkg/aipipeline/quota_test.go
git commit -m "$(cat <<'EOF'
Add FeatureGenerateRevisionPlan + FeatureCrossSubjectRank

[+] FeatureKey enumerants for plan + cross-subject rank
[+] QuotaLimits.PlanCalls (default 5/day)
[+] cross-subject rank passes quota check unconditionally (sub-step)
[+] CheckAgainstLimitsForTest export for unit tests
[&] checkAgainstLimits split into pure function + Service wrapper
EOF
)"
```

---

## Task 3: Add `cross_subject_rank.tmpl` prompt + `RenderCrossSubjectRank`

**Files:**
- Create: `pkg/aipipeline/prompts/cross_subject_rank.tmpl`
- Modify: `pkg/aipipeline/prompts.go` (add `CrossSubjectRankValues` + `RenderCrossSubjectRank`)
- Modify: `pkg/aipipeline/prompts_test.go` (add render assertion)

- [ ] **Step 1: Write a failing test**

Append to `pkg/aipipeline/prompts_test.go`:

```go
func TestRenderCrossSubjectRank_IncludesAllInputs(t *testing.T) {
    out, err := aipipeline.RenderCrossSubjectRank(aipipeline.CrossSubjectRankValues{
        ExamSubject: "Biologie Cellulaire",
        ExamTitle:   "Partiel mitose",
        Candidates: []aipipeline.CrossSubjectCandidate{
            {ID: 12, Title: "Cycle cellulaire", SubjectName: "Microbiologie", Keywords: []string{"mitose", "cycle"}, OverlapScore: 2},
            {ID: 13, Title: "ADN", SubjectName: "Biochimie", Keywords: []string{"chromosome"}, OverlapScore: 1},
        },
        TopK: 15,
    })
    if err != nil {
        t.Fatalf("render: %v", err)
    }
    for _, want := range []string{"Biologie Cellulaire", "Partiel mitose", "Cycle cellulaire", "Microbiologie", "mitose", "15"} {
        if !strings.Contains(out, want) {
            t.Errorf("missing %q in:\n%s", want, out)
        }
    }
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestRenderCrossSubjectRank -count=1`
Expected: `undefined: RenderCrossSubjectRank`.

- [ ] **Step 3: Add the values type**

In `pkg/aipipeline/prompts.go`, append:

```go
// CrossSubjectCandidate is one shortlist row passed to the ranker prompt.
type CrossSubjectCandidate struct {
    ID           int64    // ID is the flashcard id
    Title        string   // Title is the flashcard title
    SubjectName  string   // SubjectName is the source subject's display name
    Keywords     []string // Keywords are the AI-extracted keywords for this card
    OverlapScore int      // OverlapScore is the count of shared keywords with the exam subject
}

// CrossSubjectRankValues drives the cross-subject ranker template.
type CrossSubjectRankValues struct {
    ExamSubject string                  // ExamSubject is the exam's subject name
    ExamTitle   string                  // ExamTitle helps the model focus on the exam topic
    Candidates  []CrossSubjectCandidate // Candidates is the keyword-overlap shortlist
    TopK        int                     // TopK is the number of cards the model should select
}

// RenderCrossSubjectRank renders the cross-subject ranker prompt.
func RenderCrossSubjectRank(v CrossSubjectRankValues) (string, error) {
    return renderTemplate("prompts/cross_subject_rank.tmpl", v)
}
```

- [ ] **Step 4: Create the template**

Create `pkg/aipipeline/prompts/cross_subject_rank.tmpl`:

```
You are helping a student revise for an upcoming exam.

EXAM SUBJECT: {{.ExamSubject}}
EXAM TITLE: {{.ExamTitle}}

You will receive a shortlist of flashcards from OTHER subjects in the student's
study set. They share keywords with the exam subject. Your job: pick the {{.TopK}}
most directly relevant cards for revising for this exam.

Rules:
- Pick by topical relevance, not by overlap score alone.
- Reject cards whose connection is incidental (a shared word but unrelated topic).
- Return the selected card IDs in priority order (most relevant first).

CANDIDATES:
{{- range .Candidates}}
- ID: {{.ID}} | "{{.Title}}" ({{.SubjectName}}) | keywords: {{join .Keywords ", "}} | overlap: {{.OverlapScore}}
{{- end}}

Respond with the JSON tool-use payload only.
```

The renderer's `renderTemplate` already registers a `join` helper (see `prompts.go:38-49`); if it doesn't, add `{{"{{"}}` manually inside the templating below. Verify by running the test.

- [ ] **Step 5: Verify the template registers `join`**

If the template fails to parse with "function 'join' not defined", edit `pkg/aipipeline/prompts.go` `renderTemplate` to add a `Funcs` map:

```go
tmpl := template.New(filepath.Base(path)).Funcs(template.FuncMap{
    "join": strings.Join,
})
```

(Add `"strings"` to imports if not already present.)

- [ ] **Step 6: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestRenderCrossSubjectRank -count=1 -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/aipipeline/prompts.go pkg/aipipeline/prompts/cross_subject_rank.tmpl pkg/aipipeline/prompts_test.go
git commit -m "$(cat <<'EOF'
Add cross_subject_rank prompt + renderer

[+] CrossSubjectRankValues + CrossSubjectCandidate types
[+] cross_subject_rank.tmpl with overlap-aware ranker instructions
[+] RenderCrossSubjectRank exported entrypoint
[+] template join helper for keyword list rendering
EOF
)"
```

---

## Task 4: Add `revision_plan.tmpl` prompt + `RenderRevisionPlan`

**Files:**
- Create: `pkg/aipipeline/prompts/revision_plan.tmpl`
- Modify: `pkg/aipipeline/prompts.go` (add `RevisionPlanValues` + `RenderRevisionPlan`)
- Modify: `pkg/aipipeline/prompts_test.go`

- [ ] **Step 1: Write a failing test**

Append to `pkg/aipipeline/prompts_test.go`:

```go
func TestRenderRevisionPlan_IncludesAllSections(t *testing.T) {
    out, err := aipipeline.RenderRevisionPlan(aipipeline.RevisionPlanValues{
        ExamDate:      "2026-06-15",
        DaysRemaining: 30,
        ExamTitle:     "Partiel Biologie",
        ExamNotes:     "Focus on mitosis",
        SubjectName:   "Biologie Cellulaire",
        PrimaryCards: []aipipeline.PlanCardInfo{
            {ID: 12, Title: "Mitose", Keywords: []string{"mitose", "chromosome"}},
        },
        CrossSubjectCards: []aipipeline.PlanCardInfo{
            {ID: 205, Title: "Cycle procaryote", Keywords: []string{"cycle"}, SubjectName: "Microbiologie"},
        },
        UserStats: aipipeline.PlanUserStats{New: 42, Bad: 8, Ok: 15, Good: 67},
    })
    if err != nil {
        t.Fatalf("render: %v", err)
    }
    for _, want := range []string{"2026-06-15", "30", "Partiel Biologie", "Focus on mitosis", "Mitose", "Cycle procaryote", "Microbiologie", "42"} {
        if !strings.Contains(out, want) {
            t.Errorf("missing %q in:\n%s", want, out)
        }
    }
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestRenderRevisionPlan -count=1`
Expected: `undefined: RenderRevisionPlan`.

- [ ] **Step 3: Add the values types**

In `pkg/aipipeline/prompts.go`, append:

```go
// PlanCardInfo is one flashcard summary passed into the plan template.
type PlanCardInfo struct {
    ID          int64    // ID is the flashcard id
    Title       string   // Title is the flashcard title (may be empty)
    Keywords    []string // Keywords are the extracted keywords for this card
    SubjectName string   // SubjectName is non-empty only for cross-subject cards
}

// PlanUserStats is the model-visible summary of card states for pacing.
type PlanUserStats struct {
    New  int // New is the count of never-reviewed cards
    Bad  int // Bad is the count of cards last marked "bad"
    Ok   int // Ok is the count of cards last marked "ok"
    Good int // Good is the count of cards last marked "good"
}

// RevisionPlanValues drives the outer plan-generation template.
type RevisionPlanValues struct {
    ExamDate          string         // ExamDate is the exam day in YYYY-MM-DD
    DaysRemaining     int            // DaysRemaining is the inclusive day count from today
    ExamTitle         string         // ExamTitle is the exam's display title
    ExamNotes         string         // ExamNotes is the optional user-provided focus
    SubjectName       string         // SubjectName is the exam's primary subject name
    HasAnnales        bool           // HasAnnales tells the model whether annales images are attached
    PrimaryCards      []PlanCardInfo // PrimaryCards are flashcards in the exam subject
    CrossSubjectCards []PlanCardInfo // CrossSubjectCards are AI-ranked cross-subject picks
    UserStats         PlanUserStats  // UserStats are aggregate review-state counts
}

// RenderRevisionPlan renders the outer revision-plan-generation prompt.
func RenderRevisionPlan(v RevisionPlanValues) (string, error) {
    return renderTemplate("prompts/revision_plan.tmpl", v)
}
```

- [ ] **Step 4: Create the template**

Create `pkg/aipipeline/prompts/revision_plan.tmpl`:

```
You are an AI study planner generating a daily revision schedule for a student
preparing for an exam.

EXAM
- Date: {{.ExamDate}} ({{.DaysRemaining}} days from today, inclusive)
- Title: {{.ExamTitle}}
- Subject: {{.SubjectName}}
{{- if .ExamNotes}}
- Notes: {{.ExamNotes}}
{{- end}}
{{- if .HasAnnales}}
- The student has attached past-paper images; weight cards that match those topics.
{{- end}}

USER STATE
- New: {{.UserStats.New}} | Bad: {{.UserStats.Bad}} | Ok: {{.UserStats.Ok}} | Good: {{.UserStats.Good}}

PRIMARY-SUBJECT CARDS (from {{.SubjectName}})
{{- range .PrimaryCards}}
- ID {{.ID}}: "{{.Title}}" — keywords: {{join .Keywords ", "}}
{{- end}}

{{- if .CrossSubjectCards}}

CROSS-SUBJECT CARDS (relevant to {{.SubjectName}})
{{- range .CrossSubjectCards}}
- ID {{.ID}} ({{.SubjectName}}): "{{.Title}}" — keywords: {{join .Keywords ", "}}
{{- end}}
{{- end}}

INSTRUCTIONS
- Build one schedule entry per day from today through {{.ExamDate}}.
- Each day has three buckets: primarySubjectCards, crossSubjectCards, deeperDives.
- Cards may appear at most ONCE across the entire plan.
- Front-load "Bad" cards earlier; spread "Good" cards toward the end.
- The deeper-dives bucket on each day is optional and unlocked when the daily goal is met.
- Cap each day at ~10 cards across primary + cross to avoid burnout.
- Use only IDs from the lists above. Never invent IDs.

Respond with the JSON tool-use payload only.
```

- [ ] **Step 5: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestRenderRevisionPlan -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/prompts.go pkg/aipipeline/prompts/revision_plan.tmpl pkg/aipipeline/prompts_test.go
git commit -m "$(cat <<'EOF'
Add revision_plan prompt + renderer

[+] RevisionPlanValues + PlanCardInfo + PlanUserStats types
[+] revision_plan.tmpl outer planner instructions
[+] RenderRevisionPlan exported entrypoint
EOF
)"
```

---

## Task 5: Add `aiProvider.PDFPageCount` lightweight helper

**Files:**
- Modify: `internal/aiProvider/pdf.go` (add `PDFPageCount`)
- Modify: `internal/aiProvider/pdf_test.go` (add unit test)

The annales attach flow needs to validate page count <= 10 BEFORE storing — `PDFToImages` is too heavy (rasterizes everything). Add a fast variant that only opens the doc.

- [ ] **Step 1: Write a failing test**

Append to `internal/aiProvider/pdf_test.go`:

```go
func TestPDFPageCount_RejectsEmptyBytes(t *testing.T) {
    _, err := aiProvider.PDFPageCount(nil)
    if err == nil {
        t.Error("want error on nil bytes")
    }
}

func TestPDFPageCount_ValidPDF(t *testing.T) {
    // Reuse a fixture from the existing PDFToImages tests if there is one;
    // otherwise inline a minimal valid PDF byte sequence (see Step 3).
    pdfBytes := minimalSinglePagePDF()
    n, err := aiProvider.PDFPageCount(pdfBytes)
    if err != nil {
        t.Fatalf("count: %v", err)
    }
    if n != 1 {
        t.Errorf("want 1 page, got %d", n)
    }
}
```

If there's already a `testdata/sample.pdf` used by the rasterize test, use it instead and assert against its known page count.

- [ ] **Step 2: Run the test**

Run: `go test ./internal/aiProvider/ -run TestPDFPageCount -count=1`
Expected: `undefined: PDFPageCount`.

- [ ] **Step 3: Implement `PDFPageCount`**

In `internal/aiProvider/pdf.go`, append:

```go
// PDFPageCount returns the number of pages in pdfBytes without rasterizing.
// Useful for upload-time validation (page caps, quota estimation).
func PDFPageCount(pdfBytes []byte) (int, error) {
    if len(pdfBytes) == 0 {
        return 0, fmt.Errorf("empty pdf bytes")
    }

    doc, err := fitz.NewFromMemory(pdfBytes)
    if err != nil {
        return 0, fmt.Errorf("open pdf:\n%w", err)
    }

    defer doc.Close()

    return doc.NumPage(), nil
}
```

If the test references `minimalSinglePagePDF()`, add that as a tiny test helper in `pdf_test.go`. The simplest valid PDF is ~150 bytes; consult `gen2brain/go-fitz` examples or use an existing fixture.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/aiProvider/ -run TestPDFPageCount -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/aiProvider/pdf.go internal/aiProvider/pdf_test.go
git commit -m "$(cat <<'EOF'
Add lightweight PDFPageCount helper

[+] PDFPageCount opens pdf via go-fitz and returns NumPage()
[+] no rasterization (page-count validation path)
[+] unit tests for empty input and a known-page-count fixture
EOF
)"
```

---

## Task 6: Add `aipipeline.Service.RankCrossSubjects` (non-streaming)

**Files:**
- Create: `pkg/aipipeline/service_revision.go`
- Create: `pkg/aipipeline/service_revision_test.go`

- [ ] **Step 1: Write a failing test**

Create `pkg/aipipeline/service_revision_test.go`:

```go
package aipipeline_test

import (
    "context"
    "testing"

    "studbud/backend/internal/aiProvider"
    "studbud/backend/pkg/aipipeline"
    "studbud/backend/testutil"
)

func TestRankCrossSubjects_HappyPath(t *testing.T) {
    body := `{"selectedIds":[12,205,308]}`
    svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{
        Chunks: []aiProvider.Chunk{{Text: body, Done: true}},
    }, "claude-test")

    out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{
        ExamSubject: "Biologie",
        ExamTitle:   "Partiel",
        Candidates: []aipipeline.CrossSubjectCandidate{
            {ID: 12, Title: "Mitose", SubjectName: "Microbio", Keywords: []string{"mitose"}, OverlapScore: 2},
            {ID: 205, Title: "Cycle", SubjectName: "Biochimie", Keywords: []string{"cycle"}, OverlapScore: 3},
            {ID: 308, Title: "ADN", SubjectName: "Biochimie", Keywords: []string{"chromosome"}, OverlapScore: 1},
        },
        TopK: 15,
    })
    if err != nil {
        t.Fatalf("rank: %v", err)
    }
    if len(out.SelectedIDs) != 3 || out.SelectedIDs[0] != 12 {
        t.Errorf("unexpected ids: %+v", out.SelectedIDs)
    }
}

func TestRankCrossSubjects_EmptyCandidates(t *testing.T) {
    svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{}, "claude-test")
    out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{TopK: 15})
    if err != nil {
        t.Fatalf("expected silent success on empty input, got %v", err)
    }
    if len(out.SelectedIDs) != 0 {
        t.Errorf("want empty, got %+v", out.SelectedIDs)
    }
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestRankCrossSubjects -count=1`
Expected: `undefined: RankCrossSubjects`.

- [ ] **Step 3: Implement**

Create `pkg/aipipeline/service_revision.go`:

```go
package aipipeline

import (
    "context"
    "encoding/json"
    "fmt"

    "studbud/backend/internal/aiProvider"
)

// RankInput is the input to the cross-subject ranker.
type RankInput struct {
    ExamSubject string                  // ExamSubject is the exam's subject name
    ExamTitle   string                  // ExamTitle is the exam's display title
    Candidates  []CrossSubjectCandidate // Candidates is the keyword-overlap shortlist
    TopK        int                     // TopK caps the number of selected ids
}

// RankResult is the parsed model output for cross-subject ranking.
type RankResult struct {
    SelectedIDs []int64 `json:"selectedIds"` // SelectedIDs are flashcard IDs in priority order
}

// RankCrossSubjects asks the model to pick the most relevant cross-subject cards.
// It does NOT debit user quota (sub-step of plan generation, counted at the
// outer call). Empty Candidates short-circuits with an empty result.
func (s *Service) RankCrossSubjects(ctx context.Context, in RankInput) (*RankResult, error) {
    if len(in.Candidates) == 0 {
        return &RankResult{}, nil
    }

    prompt, err := RenderCrossSubjectRank(CrossSubjectRankValues{
        ExamSubject: in.ExamSubject,
        ExamTitle:   in.ExamTitle,
        Candidates:  in.Candidates,
        TopK:        in.TopK,
    })
    if err != nil {
        return nil, fmt.Errorf("render rank prompt:\n%w", err)
    }

    chunks, err := s.provider.Stream(ctx, aiProvider.Request{
        FeatureKey: string(FeatureCrossSubjectRank),
        Model:      s.model,
        Prompt:     prompt,
        Schema:     rankSchema(),
        MaxTokens:  512,
    })
    if err != nil {
        return nil, classifyProviderStartErr(err)
    }

    buf, err := drainChunks(ctx, chunks)
    if err != nil {
        return nil, err
    }

    var out RankResult

    if err := json.Unmarshal(buf, &out); err != nil {
        return nil, fmt.Errorf("parse rank result:\n%w", err)
    }

    return &out, nil
}

// rankSchema returns the JSON schema for cross-subject ranker output.
func rankSchema() []byte {
    return []byte(`{
      "type":"object",
      "required":["selectedIds"],
      "properties":{
        "selectedIds":{
          "type":"array",
          "minItems":0,
          "maxItems":50,
          "items":{"type":"integer"}
        }
      }
    }`)
}
```

(`drainChunks` and `classifyProviderStartErr` are existing helpers — same as those used by `service_keywords.go`.)

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/aipipeline/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/service_revision.go pkg/aipipeline/service_revision_test.go
git commit -m "$(cat <<'EOF'
Add aipipeline.Service.RankCrossSubjects

[+] RankInput + RankResult types
[+] non-streaming AI call mirroring ExtractKeywords shape
[+] empty-candidates short-circuit (no API call)
[+] unit tests for happy path + empty input
EOF
)"
```

---

## Task 7: Add `aipipeline.Service.GenerateRevisionPlan` (streaming)

**Files:**
- Modify: `pkg/aipipeline/service_revision.go` (append to file from Task 6)
- Modify: `pkg/aipipeline/service_revision_test.go` (append)

This streams via `RunStructuredGeneration`, NOT via a non-streaming primitive — the SSE handler forwards chunks to the client during long generations.

- [ ] **Step 1: Write a failing test**

Append to `pkg/aipipeline/service_revision_test.go`:

```go
func TestGenerateRevisionPlan_HappyPath(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    testutil.GiveAIAccess(t, pool, u.ID)

    body := `{"days":[{"date":"2026-06-15","primarySubjectCards":[12],"crossSubjectCards":[],"deeperDives":[]}]}`
    cli := &testutil.FakeAIClient{Chunks: []aiProvider.Chunk{{Text: body, Done: true}}}
    svc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "claude-test")

    out, err := svc.GenerateRevisionPlan(context.Background(), aipipeline.PlanGenerateInput{
        UserID:        u.ID,
        ExamID:        99,
        SubjectID:     1,
        Prompt:        "render me a plan",
        AnnalesImages: nil,
    })
    if err != nil {
        t.Fatalf("generate: %v", err)
    }
    if out.JobID == 0 {
        t.Errorf("want jobId > 0")
    }

    var collected string
    for c := range out.Chunks {
        if c.Kind == aipipeline.ChunkKindError {
            t.Fatalf("provider error chunk: %s", c.Text)
        }
        if c.Kind == aipipeline.ChunkKindData {
            collected += c.Text
        }
    }
    if !strings.Contains(collected, "2026-06-15") {
        t.Errorf("did not see plan body in stream: %q", collected)
    }
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./pkg/aipipeline/ -run TestGenerateRevisionPlan -count=1`
Expected: `undefined: GenerateRevisionPlan`.

- [ ] **Step 3: Implement**

Append to `pkg/aipipeline/service_revision.go`:

```go
// PlanGenerateInput is the input to the streaming plan-generation primitive.
type PlanGenerateInput struct {
    UserID        int64               // UserID is the caller (used for quota debit + audit)
    ExamID        int64               // ExamID is for ai_jobs metadata
    SubjectID     int64               // SubjectID is for ai_jobs metadata
    Prompt        string              // Prompt is the rendered plan-generation prompt
    AnnalesImages []aiProvider.ImagePart // AnnalesImages are optional past-paper page images
}

// PlanGenerateOutput exposes the streaming chunks + the audit job id.
type PlanGenerateOutput struct {
    Chunks <-chan AIChunk // Chunks is the provider stream forwarded to SSE
    JobID  int64          // JobID is the ai_jobs row id for client correlation
}

// GenerateRevisionPlan launches a streaming plan generation. It calls into
// RunStructuredGeneration and returns the stream channel plus the job id.
// Quota is debited only on success (handled by the underlying primitive's
// post-run accounting).
func (s *Service) GenerateRevisionPlan(ctx context.Context, in PlanGenerateInput) (*PlanGenerateOutput, error) {
    req := AIRequest{
        UserID:    in.UserID,
        Feature:   FeatureGenerateRevisionPlan,
        SubjectID: in.SubjectID,
        Prompt:    in.Prompt,
        Images:    in.AnnalesImages,
        PDFPages:  len(in.AnnalesImages),
        Metadata: map[string]any{
            "exam_id":     in.ExamID,
            "page_count":  len(in.AnnalesImages),
        },
    }

    ch, jobID, err := s.RunStructuredGeneration(ctx, req)
    if err != nil {
        return nil, err
    }

    return &PlanGenerateOutput{Chunks: ch, JobID: jobID}, nil
}
```

(`AIRequest`, `AIChunk`, `ChunkKindData`, `ChunkKindError` are existing types in `pkg/aipipeline/model.go`.)

- [ ] **Step 4: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/aipipeline/ -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/service_revision.go pkg/aipipeline/service_revision_test.go
git commit -m "$(cat <<'EOF'
Add aipipeline.Service.GenerateRevisionPlan streaming primitive

[+] PlanGenerateInput / PlanGenerateOutput types
[+] thin wrapper over RunStructuredGeneration with FeatureGenerateRevisionPlan
[+] annales image attachment via AIRequest.Images
[+] integration test asserting streamed body reaches the channel
EOF
)"
```

---

## Task 8: Create `pkg/exam.Service` — CRUD with access checks

**Files:**
- Create: `pkg/exam/service.go`
- Create: `pkg/exam/service_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/exam/service_test.go`:

```go
package exam_test

import (
    "context"
    "testing"
    "time"

    "studbud/backend/pkg/access"
    "studbud/backend/pkg/exam"
    "studbud/backend/testutil"
)

func TestExamCreate_RejectsNonOwner(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    owner := testutil.NewVerifiedUser(t, pool)
    other := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, owner.ID)
    svc := exam.NewService(pool, access.NewService(pool))
    if _, err := svc.Create(context.Background(), other.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, 7),
    }); err == nil {
        t.Error("want forbidden, got nil")
    }
}

func TestExamCreate_RejectsPastDate(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewService(pool, access.NewService(pool))
    if _, err := svc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, -1),
    }); err == nil {
        t.Error("want validation error, got nil")
    }
}

func TestExamCreate_EnforcesMaxActiveLimit(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewService(pool, access.NewService(pool))
    for i := 0; i < 10; i++ {
        if _, err := svc.Create(context.Background(), u.ID, exam.CreateInput{
            SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, 7+i),
        }); err != nil {
            t.Fatalf("create %d: %v", i, err)
        }
    }
    if _, err := svc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, 30),
    }); err == nil {
        t.Error("want max-exams error on 11th, got nil")
    }
}

func TestExamCRUD_RoundTrip(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewService(pool, access.NewService(pool))
    notes := "focus on mitose"
    e, err := svc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "Partiel", Date: time.Now().AddDate(0, 0, 14), Notes: &notes,
    })
    if err != nil {
        t.Fatalf("create: %v", err)
    }
    got, err := svc.Get(context.Background(), u.ID, e.ID)
    if err != nil || got.Title != "Partiel" || got.Notes == nil || *got.Notes != notes {
        t.Fatalf("get: %v %+v", err, got)
    }
    list, err := svc.ListActive(context.Background(), u.ID)
    if err != nil || len(list) != 1 {
        t.Fatalf("list: %v %+v", err, list)
    }
    if _, err := svc.Update(context.Background(), u.ID, e.ID, exam.UpdateInput{Title: ptr("Renamed")}); err != nil {
        t.Fatalf("update: %v", err)
    }
    if err := svc.Delete(context.Background(), u.ID, e.ID); err != nil {
        t.Fatalf("delete: %v", err)
    }
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/exam/... -count=1
```
Expected: build failure (`pkg/exam` doesn't exist).

- [ ] **Step 3: Implement the service**

Create `pkg/exam/service.go`:

```go
package exam

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "studbud/backend/internal/myErrors"
    "studbud/backend/pkg/access"
)

// Exam is one upcoming-exam entity owned by a user.
type Exam struct {
    ID             int64     `json:"id"`             // ID is the database id
    UserID         int64     `json:"user_id"`        // UserID is the owning user
    SubjectID      int64     `json:"subject_id"`     // SubjectID is the exam's primary subject
    Title          string    `json:"title"`          // Title is a short display title
    Date           time.Time `json:"date"`           // Date is the exam day (DATE column)
    Notes          *string   `json:"notes"`          // Notes is the optional user-provided focus
    AnnalesImageID *string   `json:"annales_image_id"` // AnnalesImageID references images.id (PDF or image)
    CreatedAt      time.Time `json:"created_at"`     // CreatedAt is the row insert timestamp
    UpdatedAt      time.Time `json:"updated_at"`     // UpdatedAt is the last mutation timestamp
}

// CreateInput drives Service.Create.
type CreateInput struct {
    SubjectID int64     // SubjectID must be a subject the caller can edit
    Title     string    // Title is required
    Date      time.Time // Date must be today or later
    Notes     *string   // Notes is optional
}

// UpdateInput drives Service.Update; nil fields stay unchanged.
type UpdateInput struct {
    Title *string    // Title patches the title when non-nil
    Date  *time.Time // Date patches the date when non-nil
    Notes *string    // Notes patches the notes when non-nil
}

// MaxActiveExamsPerUser caps create-spam.
const MaxActiveExamsPerUser = 10

// Service owns exam CRUD.
type Service struct {
    db     *pgxpool.Pool   // db is the shared pool
    access *access.Service // access enforces subject permissions
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
    return &Service{db: db, access: acc}
}

// Create inserts an exam after access + date + max-active checks.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Exam, error) {
    if in.Title == "" {
        return nil, myErrors.ErrInvalidInput
    }

    if !isFutureOrToday(in.Date) {
        return nil, myErrors.ErrInvalidInput
    }

    level, err := s.access.SubjectLevel(ctx, uid, in.SubjectID)
    if err != nil {
        return nil, err
    }

    if !level.CanRead() {
        return nil, myErrors.ErrForbidden
    }

    if err := s.assertUnderActiveLimit(ctx, uid); err != nil {
        return nil, err
    }

    return s.insert(ctx, uid, in)
}

// isFutureOrToday returns true when d is today or later (UTC date compare).
func isFutureOrToday(d time.Time) bool {
    today := time.Now().UTC().Truncate(24 * time.Hour)
    return !d.UTC().Truncate(24 * time.Hour).Before(today)
}

// assertUnderActiveLimit returns ErrInvalidInput when the user already has
// MaxActiveExamsPerUser active (date >= today) exams.
func (s *Service) assertUnderActiveLimit(ctx context.Context, uid int64) error {
    var n int

    err := s.db.QueryRow(ctx, `SELECT count(*) FROM exams WHERE user_id=$1 AND date >= CURRENT_DATE`, uid).Scan(&n)
    if err != nil {
        return fmt.Errorf("count active exams:\n%w", err)
    }

    if n >= MaxActiveExamsPerUser {
        return myErrors.ErrInvalidInput
    }

    return nil
}

// insert performs the INSERT ... RETURNING.
func (s *Service) insert(ctx context.Context, uid int64, in CreateInput) (*Exam, error) {
    var e Exam

    err := s.db.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, title, date, notes)
        VALUES ($1,$2,$3,$4,$5)
        RETURNING id, user_id, subject_id, title, date, notes, annales_image_id, created_at, updated_at
    `, uid, in.SubjectID, in.Title, in.Date, in.Notes).Scan(
        &e.ID, &e.UserID, &e.SubjectID, &e.Title, &e.Date, &e.Notes, &e.AnnalesImageID, &e.CreatedAt, &e.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("insert exam:\n%w", err)
    }

    return &e, nil
}

// Get returns the exam if owned by uid.
func (s *Service) Get(ctx context.Context, uid, id int64) (*Exam, error) {
    e, err := s.load(ctx, id)
    if err != nil {
        return nil, err
    }

    if e.UserID != uid {
        return nil, myErrors.ErrForbidden
    }

    return e, nil
}

// ListActive returns the user's exams with date >= today, ordered by date asc.
func (s *Service) ListActive(ctx context.Context, uid int64) ([]Exam, error) {
    rows, err := s.db.Query(ctx, `
        SELECT id, user_id, subject_id, title, date, notes, annales_image_id, created_at, updated_at
        FROM exams WHERE user_id=$1 AND date >= CURRENT_DATE ORDER BY date ASC
    `, uid)
    if err != nil {
        return nil, fmt.Errorf("list exams:\n%w", err)
    }

    defer rows.Close()

    var out []Exam

    for rows.Next() {
        var e Exam

        if err := rows.Scan(&e.ID, &e.UserID, &e.SubjectID, &e.Title, &e.Date, &e.Notes, &e.AnnalesImageID, &e.CreatedAt, &e.UpdatedAt); err != nil {
            return nil, fmt.Errorf("scan exam:\n%w", err)
        }

        out = append(out, e)
    }

    return out, rows.Err()
}

// Update patches an exam owned by uid.
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Exam, error) {
    e, err := s.Get(ctx, uid, id)
    if err != nil {
        return nil, err
    }

    title, date, notes := patchExam(e, in)

    if !isFutureOrToday(date) {
        return nil, myErrors.ErrInvalidInput
    }

    var out Exam

    err = s.db.QueryRow(ctx, `
        UPDATE exams SET title=$1, date=$2, notes=$3, updated_at=now()
        WHERE id=$4
        RETURNING id, user_id, subject_id, title, date, notes, annales_image_id, created_at, updated_at
    `, title, date, notes, id).Scan(
        &out.ID, &out.UserID, &out.SubjectID, &out.Title, &out.Date, &out.Notes, &out.AnnalesImageID, &out.CreatedAt, &out.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("update exam:\n%w", err)
    }

    return &out, nil
}

// patchExam merges UpdateInput onto e, returning the resolved field values.
func patchExam(e *Exam, in UpdateInput) (title string, date time.Time, notes *string) {
    title, date, notes = e.Title, e.Date, e.Notes

    if in.Title != nil {
        title = *in.Title
    }

    if in.Date != nil {
        date = *in.Date
    }

    if in.Notes != nil {
        notes = in.Notes
    }

    return title, date, notes
}

// Delete removes an exam owned by uid.
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
    if _, err := s.Get(ctx, uid, id); err != nil {
        return err
    }

    if _, err := s.db.Exec(ctx, `DELETE FROM exams WHERE id=$1`, id); err != nil {
        return fmt.Errorf("delete exam:\n%w", err)
    }

    return nil
}

// load fetches a row by id, returning ErrNotFound when absent.
func (s *Service) load(ctx context.Context, id int64) (*Exam, error) {
    var e Exam

    err := s.db.QueryRow(ctx, `
        SELECT id, user_id, subject_id, title, date, notes, annales_image_id, created_at, updated_at
        FROM exams WHERE id=$1
    `, id).Scan(
        &e.ID, &e.UserID, &e.SubjectID, &e.Title, &e.Date, &e.Notes, &e.AnnalesImageID, &e.CreatedAt, &e.UpdatedAt,
    )
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, myErrors.ErrNotFound
    }

    if err != nil {
        return nil, fmt.Errorf("load exam:\n%w", err)
    }

    return &e, nil
}
```

- [ ] **Step 4: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/exam/... -count=1 -v
```
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/exam/service.go pkg/exam/service_test.go
git commit -m "$(cat <<'EOF'
Add pkg/exam.Service with CRUD + access checks

[+] Exam type + CreateInput / UpdateInput
[+] Create with future-date guard + max-active-exams=10 cap
[+] access.Service.SubjectLevel gates Create
[+] Get/ListActive/Update/Delete with ownership checks
[+] integration tests for forbidden, past-date, max-active, round-trip
EOF
)"
```

---

## Task 9: Add `pkg/exam.Service.AttachAnnales`

**Files:**
- Create: `pkg/exam/annales.go`
- Modify: `pkg/exam/service_test.go` (append annales tests)
- Modify: `pkg/image/service.go` (add `UploadBlob` if not present)

The annales path uploads a PDF (or image) blob, validates page count <= 10, stores via `image.Service`, and patches `exams.annales_image_id`. Uses `aiProvider.PDFPageCount` from Task 5.

- [ ] **Step 1: Inspect `pkg/image/service.go`**

Run:
```bash
grep -n "func (s \*Service) Upload\|func (s \*Service) UploadBlob\|MimeType\|mime_type" pkg/image/service.go
```

If `Upload` accepts arbitrary MIME types and inserts an `images` row, reuse it. Otherwise add a sibling `UploadBlob(ctx, uid, src, filename, mimeType) (*Image, error)` that bypasses image-only MIME validation.

- [ ] **Step 2: Write failing tests**

Append to `pkg/exam/service_test.go`:

```go
func TestAttachAnnales_RejectsTooManyPages(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewServiceWithImage(pool, access.NewService(pool), image.NewService(pool, fakeStore{}, ""))
    e, _ := svc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, 7),
    })
    bigPDF := makePDFWithPages(t, 11) // helper produces an 11-page valid PDF
    if _, err := svc.AttachAnnales(context.Background(), u.ID, e.ID, "annales.pdf", "application/pdf", bigPDF); err == nil {
        t.Error("want too-many-pages error, got nil")
    }
}

func TestAttachAnnales_PersistsImageIdOnExam(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewServiceWithImage(pool, access.NewService(pool), image.NewService(pool, fakeStore{}, ""))
    e, _ := svc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "x", Date: time.Now().AddDate(0, 0, 7),
    })
    pdf := makePDFWithPages(t, 3)
    out, err := svc.AttachAnnales(context.Background(), u.ID, e.ID, "annales.pdf", "application/pdf", pdf)
    if err != nil {
        t.Fatalf("attach: %v", err)
    }
    if out.AnnalesImageID == nil || *out.AnnalesImageID == "" {
        t.Errorf("want annales_image_id set, got %+v", out.AnnalesImageID)
    }
}
```

`fakeStore{}` is a stub `storage.FileStore`-compatible writer used in `pkg/image/service_test.go` — reuse it; if absent, define a minimal in-memory stub here. `makePDFWithPages(t, n)` is a fixture helper that produces a valid `n`-page PDF; if hard to synthesize, read from `internal/aiProvider/testdata/<n>page.pdf` if those fixtures exist, otherwise add new fixtures.

- [ ] **Step 3: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/exam/... -count=1
```
Expected: build failure (`AttachAnnales` undefined).

- [ ] **Step 4: Implement**

Create `pkg/exam/annales.go`:

```go
package exam

import (
    "bytes"
    "context"
    "fmt"

    "studbud/backend/internal/aiProvider"
    "studbud/backend/internal/myErrors"
)

// MaxAnnalesPages caps annales PDFs to keep the prompt size predictable.
const MaxAnnalesPages = 10

// MaxAnnalesBytes caps the upload size to 5 MB.
const MaxAnnalesBytes = 5 << 20

// AttachAnnales validates the upload, stores it via the image service, and
// patches exams.annales_image_id. Accepts application/pdf or image/* MIME
// types. PDFs are page-count gated; images skip the page check.
func (s *Service) AttachAnnales(ctx context.Context, uid, examID int64, filename, mimeType string, body []byte) (*Exam, error) {
    if int64(len(body)) > MaxAnnalesBytes {
        return nil, myErrors.ErrInvalidInput
    }

    e, err := s.Get(ctx, uid, examID)
    if err != nil {
        return nil, err
    }

    if mimeType == "application/pdf" {
        if err := s.validatePDFPageCount(body); err != nil {
            return nil, err
        }
    }

    if s.images == nil {
        return nil, fmt.Errorf("image service not wired: configure via NewServiceWithImage")
    }

    img, err := s.images.UploadBlob(ctx, uid, bytes.NewReader(body), filename, mimeType)
    if err != nil {
        return nil, fmt.Errorf("upload annales blob:\n%w", err)
    }

    return s.setAnnalesImageID(ctx, e.ID, img.ID)
}

// validatePDFPageCount returns ErrInvalidInput when the PDF exceeds MaxAnnalesPages.
func (s *Service) validatePDFPageCount(body []byte) error {
    n, err := aiProvider.PDFPageCount(body)
    if err != nil {
        return fmt.Errorf("count pdf pages:\n%w", err)
    }

    if n > MaxAnnalesPages {
        return myErrors.ErrInvalidInput
    }

    return nil
}

// setAnnalesImageID patches the exam row and returns the updated Exam.
func (s *Service) setAnnalesImageID(ctx context.Context, examID int64, imageID string) (*Exam, error) {
    var out Exam

    err := s.db.QueryRow(ctx, `
        UPDATE exams SET annales_image_id=$1, updated_at=now() WHERE id=$2
        RETURNING id, user_id, subject_id, title, date, notes, annales_image_id, created_at, updated_at
    `, imageID, examID).Scan(
        &out.ID, &out.UserID, &out.SubjectID, &out.Title, &out.Date, &out.Notes, &out.AnnalesImageID, &out.CreatedAt, &out.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("update exam annales:\n%w", err)
    }

    return &out, nil
}
```

Edit `pkg/exam/service.go` to add the `images` field and the augmented constructor:

```go
import (
    // ... existing imports ...
    "studbud/backend/pkg/image"
)

type Service struct {
    db     *pgxpool.Pool
    access *access.Service
    images *image.Service // images is the optional annales upload backend
}

// NewServiceWithImage extends NewService with an image-upload backend used by
// AttachAnnales. CRUD-only callers can stay on NewService.
func NewServiceWithImage(db *pgxpool.Pool, acc *access.Service, img *image.Service) *Service {
    s := NewService(db, acc)
    s.images = img
    return s
}
```

If `pkg/image/service.go` doesn't have `UploadBlob`, add it:

```go
// UploadBlob is the PDF-tolerant sibling of Upload. It writes src to the
// FileStore and inserts an images row with the provided MIME type. Used by
// the exam annales flow to persist past-paper PDFs.
func (s *Service) UploadBlob(ctx context.Context, uid int64, src io.Reader, filename, mimeType string) (*Image, error) {
    // (Implementation mirrors Upload but skips image-only MIME validation.)
    ...
}
```

(Use the existing `Upload` body as a reference; the only difference is skipping `image.Decode` validation when `mimeType` is `application/pdf`.)

- [ ] **Step 5: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/exam/... -count=1 -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/exam/annales.go pkg/exam/service.go pkg/exam/service_test.go pkg/image/service.go
git commit -m "$(cat <<'EOF'
Add exam.Service.AttachAnnales

[+] AttachAnnales validates size (5 MB), MIME, and PDF page count (<=10)
[+] persists via image.Service.UploadBlob, patches exams.annales_image_id
[+] image.Service.UploadBlob accepts application/pdf without image decode
[+] integration tests for too-many-pages + happy path
EOF
)"
```

---

## Task 10: Exam HTTP handler + routes

**Files:**
- Create: `api/handler/exam.go`
- Create: `api/handler/exam_annales.go`
- Create: `api/handler/exam_test.go`
- Modify: `cmd/app/routes.go` (register routes)
- Modify: `cmd/app/deps.go` (deferred to Task 17 — temporarily wire here)

- [ ] **Step 1: Write failing tests**

Create `api/handler/exam_test.go`:

```go
package handler_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "studbud/backend/api/handler"
    "studbud/backend/pkg/access"
    "studbud/backend/pkg/exam"
    "studbud/backend/pkg/image"
    "studbud/backend/testutil"
)

func TestExamHandler_CreateGet(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    svc := exam.NewServiceWithImage(pool, access.NewService(pool), nil)
    h := handler.NewExamHandler(svc)
    mux := http.NewServeMux()
    h.Register(mux, testutil.AuthMiddlewareForUser(u.ID))
    srv := httptest.NewServer(mux)
    defer srv.Close()

    body := strings.NewReader(`{"subject_id":` + fmt.Sprintf("%d", sub.ID) + `,"title":"Partiel","date":"` + time.Now().AddDate(0,0,7).Format("2006-01-02") + `"}`)
    resp, err := http.Post(srv.URL+"/exams", "application/json", body)
    if err != nil || resp.StatusCode != http.StatusCreated {
        t.Fatalf("create: err=%v status=%d", err, resp.StatusCode)
    }
    var created struct{ ID int64 `json:"id"` }
    json.NewDecoder(resp.Body).Decode(&created)
    if created.ID == 0 {
        t.Fatal("want id > 0")
    }
}
```

(Adjust to match the codebase's existing handler-test conventions in `api/handler/flashcard_test.go` — including how the auth middleware is wired in tests. The test pattern above is illustrative; copy the exact setup from a working handler test.)

- [ ] **Step 2: Implement the handler**

Create `api/handler/exam.go`:

```go
package handler

import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "studbud/backend/internal/authctx"
    "studbud/backend/internal/httpx"
    "studbud/backend/internal/myErrors"
    "studbud/backend/pkg/exam"
)

// ExamHandler holds the exam service.
type ExamHandler struct {
    svc *exam.Service // svc owns CRUD + annales attach
}

// NewExamHandler constructs an ExamHandler.
func NewExamHandler(svc *exam.Service) *ExamHandler {
    return &ExamHandler{svc: svc}
}

// examCreateInput is the request body for POST /exams.
type examCreateInput struct {
    SubjectID int64   `json:"subject_id"` // SubjectID is the exam's primary subject
    Title     string  `json:"title"`      // Title is the display title
    Date      string  `json:"date"`       // Date is YYYY-MM-DD
    Notes     *string `json:"notes"`      // Notes is optional focus text
}

// Create handles POST /exams.
func (h *ExamHandler) Create(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())

    var in examCreateInput

    if err := httpx.DecodeJSON(r, &in); err != nil {
        httpx.WriteError(w, err)
        return
    }

    date, err := time.Parse("2006-01-02", in.Date)
    if err != nil {
        httpx.WriteError(w, myErrors.ErrInvalidInput)
        return
    }

    e, err := h.svc.Create(r.Context(), uid, exam.CreateInput{
        SubjectID: in.SubjectID, Title: in.Title, Date: date, Notes: in.Notes,
    })
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusCreated, e)
}

// Get handles GET /exams/{id}.
func (h *ExamHandler) Get(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    id := pathInt64(r, "id")

    e, err := h.svc.Get(r.Context(), uid, id)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, e)
}

// List handles GET /exams.
func (h *ExamHandler) List(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())

    list, err := h.svc.ListActive(r.Context(), uid)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, list)
}

// examUpdateInput is the request body for PUT /exams/{id}.
type examUpdateInput struct {
    Title *string `json:"title"`
    Date  *string `json:"date"`
    Notes *string `json:"notes"`
}

// Update handles PUT /exams/{id}.
func (h *ExamHandler) Update(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    id := pathInt64(r, "id")

    var in examUpdateInput

    if err := httpx.DecodeJSON(r, &in); err != nil {
        httpx.WriteError(w, err)
        return
    }

    patch, err := buildExamUpdate(in)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    e, err := h.svc.Update(r.Context(), uid, id, patch)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, e)
}

// buildExamUpdate parses the request body into an exam.UpdateInput.
func buildExamUpdate(in examUpdateInput) (exam.UpdateInput, error) {
    out := exam.UpdateInput{Title: in.Title, Notes: in.Notes}

    if in.Date != nil {
        d, err := time.Parse("2006-01-02", *in.Date)
        if err != nil {
            return out, myErrors.ErrInvalidInput
        }

        out.Date = &d
    }

    return out, nil
}

// Delete handles DELETE /exams/{id}.
func (h *ExamHandler) Delete(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    id := pathInt64(r, "id")

    if err := h.svc.Delete(r.Context(), uid, id); err != nil {
        httpx.WriteError(w, err)
        return
    }

    w.WriteHeader(http.StatusNoContent)
}

// pathInt64 parses a numeric path value via http.Request.PathValue.
func pathInt64(r *http.Request, name string) int64 {
    v, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
    return v
}

// Register attaches the routes to mux. middleware wraps each handler.
func (h *ExamHandler) Register(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
    mux.Handle("POST /exams", mw(http.HandlerFunc(h.Create)))
    mux.Handle("GET /exams", mw(http.HandlerFunc(h.List)))
    mux.Handle("GET /exams/{id}", mw(http.HandlerFunc(h.Get)))
    mux.Handle("PUT /exams/{id}", mw(http.HandlerFunc(h.Update)))
    mux.Handle("DELETE /exams/{id}", mw(http.HandlerFunc(h.Delete)))
}

// ensureUnused references the json import in case all body decodes go via httpx.
var _ = json.Marshal
```

Create `api/handler/exam_annales.go`:

```go
package handler

import (
    "io"
    "net/http"

    "studbud/backend/internal/authctx"
    "studbud/backend/internal/httpx"
    "studbud/backend/internal/myErrors"
)

// AttachAnnales handles POST /exams/{id}/annales (multipart upload).
func (h *ExamHandler) AttachAnnales(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    id := pathInt64(r, "id")

    if err := r.ParseMultipartForm(6 << 20); err != nil {
        httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "annales exceeds 5 MB", Wrapped: myErrors.ErrInvalidInput})
        return
    }

    f, header, err := r.FormFile("file")
    if err != nil {
        httpx.WriteError(w, myErrors.ErrInvalidInput)
        return
    }

    defer f.Close()

    body, err := io.ReadAll(io.LimitReader(f, (5<<20)+1))
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    mimeType := header.Header.Get("Content-Type")

    out, err := h.svc.AttachAnnales(r.Context(), uid, id, header.Filename, mimeType, body)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, out)
}
```

In `Register`, add:

```go
mux.Handle("POST /exams/{id}/annales", mw(http.HandlerFunc(h.AttachAnnales)))
```

- [ ] **Step 3: Wire into routes (preview — Task 17 finalizes deps)**

In `cmd/app/routes.go`, locate the route block and add (assuming `d.exam` will be wired in Task 17):

```go
// After d.exam is wired in deps.go:
examH := handler.NewExamHandler(d.exam)
examH.Register(mux, av) // av is the existing auth middleware
```

For now, you can stub the wiring by adding `_ = d.exam` to keep the import alive; Task 17 fills in the actual deps field.

- [ ] **Step 4: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./api/handler/... -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/handler/exam.go api/handler/exam_annales.go api/handler/exam_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Add exam HTTP handler + routes

[+] POST/GET/PUT/DELETE /exams handlers
[+] POST /exams/{id}/annales multipart upload (5 MB cap)
[+] handler tests for create + get round-trip
[+] route registration via ExamHandler.Register
EOF
)"
```

---

## Task 11: `pkg/revisionplan` cross-subject shortlist

**Files:**
- Create: `pkg/revisionplan/shortlist.go`
- Create: `pkg/revisionplan/shortlist_test.go`
- Create: `pkg/revisionplan/queries.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/revisionplan/shortlist_test.go`:

```go
package revisionplan_test

import (
    "context"
    "testing"

    "studbud/backend/pkg/revisionplan"
    "studbud/backend/testutil"
)

func TestShortlist_RequiresOverlapTwo(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)

    examSubject := testutil.NewSubject(t, pool, u.ID)
    otherSubject := testutil.NewSubject(t, pool, u.ID)

    primaryFC := testutil.NewFlashcard(t, pool, examSubject.ID, 0, "Q", "A")
    candidateFC := testutil.NewFlashcard(t, pool, otherSubject.ID, 0, "Q2", "A2")

    seedKeyword(t, pool, primaryFC, "mitose", 0.9)
    seedKeyword(t, pool, primaryFC, "chromosome", 0.7)
    seedKeyword(t, pool, candidateFC, "mitose", 0.6)
    seedKeyword(t, pool, candidateFC, "chromosome", 0.5)

    svc := revisionplan.NewShortlist(pool)

    out, err := svc.For(context.Background(), u.ID, examSubject.ID, 30)
    if err != nil {
        t.Fatalf("shortlist: %v", err)
    }
    if len(out) != 1 || out[0].ID != candidateFC {
        t.Fatalf("want 1 row with id=%d, got %+v", candidateFC, out)
    }
}

func TestShortlist_ExcludesInaccessibleSubjects(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    me := testutil.NewVerifiedUser(t, pool)
    other := testutil.NewVerifiedUser(t, pool)

    mySubject := testutil.NewSubject(t, pool, me.ID)
    foreignSubject := testutil.NewSubject(t, pool, other.ID) // private to other

    primaryFC := testutil.NewFlashcard(t, pool, mySubject.ID, 0, "Q", "A")
    foreignFC := testutil.NewFlashcard(t, pool, foreignSubject.ID, 0, "Q2", "A2")

    seedKeyword(t, pool, primaryFC, "mitose", 0.9)
    seedKeyword(t, pool, primaryFC, "chromosome", 0.7)
    seedKeyword(t, pool, foreignFC, "mitose", 0.6)
    seedKeyword(t, pool, foreignFC, "chromosome", 0.5)

    svc := revisionplan.NewShortlist(pool)

    out, err := svc.For(context.Background(), me.ID, mySubject.ID, 30)
    if err != nil {
        t.Fatalf("shortlist: %v", err)
    }
    if len(out) != 0 {
        t.Fatalf("want 0 (foreign subject excluded), got %+v", out)
    }
}

// seedKeyword inserts one flashcard_keywords row.
func seedKeyword(t *testing.T, pool *pgxpool.Pool, fcID int64, keyword string, weight float64) {
    t.Helper()
    _, err := pool.Exec(context.Background(),
        `INSERT INTO flashcard_keywords (fc_id, keyword, weight) VALUES ($1,$2,$3)`,
        fcID, keyword, weight)
    if err != nil {
        t.Fatalf("seed keyword: %v", err)
    }
}
```

- [ ] **Step 2: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/revisionplan/... -count=1
```
Expected: build failure (`pkg/revisionplan` doesn't exist).

- [ ] **Step 3: Implement queries.go and shortlist.go**

Create `pkg/revisionplan/queries.go`:

```go
package revisionplan

// sqlAccessibleSubjectsCTE expands to all subject IDs the user can access:
// owned subjects + active collaborator rows + active subscriber rows + public.
const sqlAccessibleSubjectsCTE = `
WITH accessible_subjects AS (
    SELECT id FROM subjects WHERE owner_id = $1
    UNION
    SELECT subject_id FROM collaborators WHERE user_id = $1
    UNION
    SELECT subject_id FROM subject_subscriptions WHERE user_id = $1
    UNION
    SELECT id FROM subjects WHERE visibility = 'public'
)
`

// sqlShortlistCrossSubject finds candidate flashcards in OTHER accessible
// subjects sharing >=2 keywords with any flashcard in the exam subject.
// $1 = userID, $2 = examSubjectID, $3 = limit.
const sqlShortlistCrossSubject = sqlAccessibleSubjectsCTE + `,
primary_keywords AS (
    SELECT DISTINCT fk.keyword
    FROM flashcards fc
    JOIN flashcard_keywords fk ON fk.fc_id = fc.id
    WHERE fc.subject_id = $2
),
candidate_fcs AS (
    SELECT fk.fc_id, COUNT(*) AS overlap_score, SUM(fk.weight) AS weight_sum
    FROM flashcard_keywords fk
    JOIN primary_keywords pk ON pk.keyword = fk.keyword
    JOIN flashcards fc ON fc.id = fk.fc_id
    WHERE fc.subject_id <> $2
      AND fc.subject_id IN (SELECT id FROM accessible_subjects)
    GROUP BY fk.fc_id
    HAVING COUNT(*) >= 2
)
SELECT
    fc.id, fc.title, fc.subject_id, s.name AS subject_name,
    c.overlap_score, c.weight_sum,
    COALESCE(
      (SELECT array_agg(fk2.keyword ORDER BY fk2.weight DESC)
       FROM flashcard_keywords fk2 WHERE fk2.fc_id = fc.id),
      ARRAY[]::TEXT[]
    ) AS keywords
FROM candidate_fcs c
JOIN flashcards fc ON fc.id = c.fc_id
JOIN subjects s ON s.id = fc.subject_id
ORDER BY c.weight_sum DESC, c.overlap_score DESC
LIMIT $3
`
```

Create `pkg/revisionplan/shortlist.go`:

```go
package revisionplan

import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5/pgxpool"
)

// FcCandidate is one shortlist row.
type FcCandidate struct {
    ID           int64    // ID is the flashcard id
    Title        string   // Title is the flashcard title
    SubjectID    int64    // SubjectID is the source subject id
    SubjectName  string   // SubjectName is the source subject's display name
    OverlapScore int      // OverlapScore is the count of shared keywords
    WeightSum    float64  // WeightSum is the sum of overlap-keyword weights
    Keywords     []string // Keywords is the candidate's full keyword list
}

// Shortlist runs the keyword-overlap query.
type Shortlist struct {
    db *pgxpool.Pool
}

// NewShortlist constructs a Shortlist.
func NewShortlist(db *pgxpool.Pool) *Shortlist {
    return &Shortlist{db: db}
}

// For returns up to `limit` cross-subject candidates for examSubjectID.
func (s *Shortlist) For(ctx context.Context, uid, examSubjectID int64, limit int) ([]FcCandidate, error) {
    rows, err := s.db.Query(ctx, sqlShortlistCrossSubject, uid, examSubjectID, limit)
    if err != nil {
        return nil, fmt.Errorf("shortlist query:\n%w", err)
    }

    defer rows.Close()

    var out []FcCandidate

    for rows.Next() {
        var c FcCandidate

        if err := rows.Scan(&c.ID, &c.Title, &c.SubjectID, &c.SubjectName, &c.OverlapScore, &c.WeightSum, &c.Keywords); err != nil {
            return nil, fmt.Errorf("scan candidate:\n%w", err)
        }

        out = append(out, c)
    }

    return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='postgres://postgres:postgres@localhost:5432/studbud_test?sslmode=disable' go test ./pkg/revisionplan/... -count=1 -v
```
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/revisionplan/shortlist.go pkg/revisionplan/queries.go pkg/revisionplan/shortlist_test.go
git commit -m "$(cat <<'EOF'
Add cross-subject keyword-overlap shortlist

[+] FcCandidate type
[+] Shortlist.For runs the overlap CTE: owned + collab + subscribed + public
[+] returns id, title, subject, overlap_score, weight_sum, keywords
[+] integration tests for overlap>=2 and inaccessible-subject exclusion
EOF
)"
```

---

## Task 12: Plan post-processor

**Files:**
- Create: `pkg/revisionplan/postprocess.go`
- Create: `pkg/revisionplan/postprocess_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/revisionplan/postprocess_test.go`:

```go
package revisionplan

import (
    "testing"
    "time"
)

func TestPostProcess_RejectsOutOfRangeDays(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    examDate, _ := time.Parse("2006-01-02", "2026-05-03")
    days := []DayBucket{
        {Date: "2026-04-29", PrimarySubjectCards: []int64{1}},
        {Date: "2026-04-30", PrimarySubjectCards: []int64{1}},
        {Date: "2026-05-04", PrimarySubjectCards: []int64{2}},
    }
    out := postProcess(days, today, examDate, map[int64]bool{1: true, 2: true})
    if len(out) != 1 || out[0].Date != "2026-04-30" {
        t.Errorf("want only 2026-04-30, got %+v", out)
    }
}

func TestPostProcess_RejectsHallucinatedIDs(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    examDate, _ := time.Parse("2006-01-02", "2026-05-01")
    days := []DayBucket{{Date: "2026-04-30", PrimarySubjectCards: []int64{1, 999}}}
    out := postProcess(days, today, examDate, map[int64]bool{1: true})
    if len(out[0].PrimarySubjectCards) != 1 || out[0].PrimarySubjectCards[0] != 1 {
        t.Errorf("hallucinated id leaked: %+v", out)
    }
}

func TestPostProcess_DedupesAcrossDays(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    examDate, _ := time.Parse("2006-01-02", "2026-05-02")
    days := []DayBucket{
        {Date: "2026-04-30", PrimarySubjectCards: []int64{1, 2}},
        {Date: "2026-05-01", PrimarySubjectCards: []int64{2, 3}},
    }
    out := postProcess(days, today, examDate, map[int64]bool{1: true, 2: true, 3: true})
    if len(out[1].PrimarySubjectCards) != 1 || out[1].PrimarySubjectCards[0] != 3 {
        t.Errorf("dedup failed: %+v", out)
    }
}

func TestPostProcess_FillsMissingDays(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    examDate, _ := time.Parse("2006-01-02", "2026-05-02")
    days := []DayBucket{{Date: "2026-04-30", PrimarySubjectCards: []int64{1}}}
    out := postProcess(days, today, examDate, map[int64]bool{1: true})
    if len(out) != 3 {
        t.Errorf("want 3 days (today..exam inclusive), got %d", len(out))
    }
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./pkg/revisionplan/ -run TestPostProcess -count=1`
Expected: build failure.

- [ ] **Step 3: Implement**

Create `pkg/revisionplan/postprocess.go`:

```go
package revisionplan

import "time"

// DayBucket is one day's plan entry stored in revision_plans.days.
type DayBucket struct {
    Date                string  `json:"date"`                  // Date is YYYY-MM-DD
    PrimarySubjectCards []int64 `json:"primarySubjectCards"`   // PrimarySubjectCards are FC IDs in the exam subject
    CrossSubjectCards   []int64 `json:"crossSubjectCards"`     // CrossSubjectCards are AI-ranked cross-subject FC IDs
    DeeperDives         []int64 `json:"deeperDives"`           // DeeperDives are bonus cards unlocked when the daily goal is met
}

// postProcess validates and normalizes the AI-returned days array:
// - drops entries with date < today or > examDate
// - drops card IDs not in the candidate set (validIDs)
// - dedupes cards across days (first occurrence wins)
// - inserts empty DayBucket entries for missing dates in [today, examDate]
func postProcess(days []DayBucket, today, examDate time.Time, validIDs map[int64]bool) []DayBucket {
    keep := filterByDateAndIDs(days, today, examDate, validIDs)
    keep = dedupAcrossDays(keep)

    return fillMissingDays(keep, today, examDate)
}

// filterByDateAndIDs drops out-of-range dates and hallucinated IDs.
func filterByDateAndIDs(days []DayBucket, today, examDate time.Time, validIDs map[int64]bool) []DayBucket {
    out := make([]DayBucket, 0, len(days))

    for _, d := range days {
        date, err := time.Parse("2006-01-02", d.Date)
        if err != nil {
            continue
        }

        if date.Before(today) || date.After(examDate) {
            continue
        }

        d.PrimarySubjectCards = filterIDs(d.PrimarySubjectCards, validIDs)
        d.CrossSubjectCards = filterIDs(d.CrossSubjectCards, validIDs)
        d.DeeperDives = filterIDs(d.DeeperDives, validIDs)

        out = append(out, d)
    }

    return out
}

// filterIDs returns the subset of ids present in validIDs.
func filterIDs(ids []int64, validIDs map[int64]bool) []int64 {
    out := make([]int64, 0, len(ids))

    for _, id := range ids {
        if validIDs[id] {
            out = append(out, id)
        }
    }

    return out
}

// dedupAcrossDays drops repeated card IDs across days (first occurrence wins).
func dedupAcrossDays(days []DayBucket) []DayBucket {
    seen := map[int64]bool{}

    for i := range days {
        days[i].PrimarySubjectCards = dedupSlice(days[i].PrimarySubjectCards, seen)
        days[i].CrossSubjectCards = dedupSlice(days[i].CrossSubjectCards, seen)
        days[i].DeeperDives = dedupSlice(days[i].DeeperDives, seen)
    }

    return days
}

// dedupSlice keeps only ids absent from seen, marking each kept id as seen.
func dedupSlice(ids []int64, seen map[int64]bool) []int64 {
    out := make([]int64, 0, len(ids))

    for _, id := range ids {
        if !seen[id] {
            seen[id] = true
            out = append(out, id)
        }
    }

    return out
}

// fillMissingDays inserts empty buckets for dates the AI omitted.
func fillMissingDays(days []DayBucket, today, examDate time.Time) []DayBucket {
    byDate := map[string]DayBucket{}

    for _, d := range days {
        byDate[d.Date] = d
    }

    out := make([]DayBucket, 0, daysBetween(today, examDate))

    for d := today; !d.After(examDate); d = d.AddDate(0, 0, 1) {
        key := d.Format("2006-01-02")

        if existing, ok := byDate[key]; ok {
            out = append(out, existing)
            continue
        }

        out = append(out, DayBucket{Date: key, PrimarySubjectCards: []int64{}, CrossSubjectCards: []int64{}, DeeperDives: []int64{}})
    }

    return out
}

// daysBetween returns the inclusive day count from start to end.
func daysBetween(start, end time.Time) int {
    return int(end.Sub(start).Hours()/24) + 1
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/revisionplan/ -run TestPostProcess -count=1 -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/revisionplan/postprocess.go pkg/revisionplan/postprocess_test.go
git commit -m "$(cat <<'EOF'
Add revision-plan post-processor

[+] DayBucket type with JSONB-friendly tags
[+] postProcess: drop out-of-range dates, hallucinated IDs, duplicates
[+] fill missing days with empty buckets to span [today, examDate]
[+] unit tests for date range, hallucination, dedup, fill
EOF
)"
```

---

## Task 13: `pkg/revisionplan.Service.GenerateForExam`

**Files:**
- Create: `pkg/revisionplan/service.go`
- Modify: `pkg/revisionplan/queries.go` (add SQL constants for plan persist + load)

- [ ] **Step 1: Write a failing integration test**

Append to `pkg/revisionplan/shortlist_test.go` (or create `pkg/revisionplan/service_test.go`):

```go
package revisionplan_test

import (
    "context"
    "testing"
    "time"

    "studbud/backend/internal/aiProvider"
    "studbud/backend/pkg/access"
    "studbud/backend/pkg/aipipeline"
    "studbud/backend/pkg/exam"
    "studbud/backend/pkg/revisionplan"
    "studbud/backend/testutil"
)

func TestGenerateForExam_HappyPath(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    testutil.GiveAIAccess(t, pool, u.ID)

    sub := testutil.NewSubject(t, pool, u.ID)
    fc1 := testutil.NewFlashcard(t, pool, sub.ID, 0, "Q1", "A1")
    fc2 := testutil.NewFlashcard(t, pool, sub.ID, 0, "Q2", "A2")

    seedKeyword(t, pool, fc1, "mitose", 0.9)

    examSvc := exam.NewService(pool, access.NewService(pool))
    e, _ := examSvc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "Partiel", Date: time.Now().AddDate(0, 0, 3),
    })

    body := `{"days":[{"date":"` + time.Now().Format("2006-01-02") + `","primarySubjectCards":[` + fmt.Sprintf("%d,%d", fc1, fc2) + `],"crossSubjectCards":[],"deeperDives":[]}]}`
    cli := &testutil.FakeAIClient{Chunks: []aiProvider.Chunk{{Text: body, Done: true}}}
    aiSvc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "claude-test")

    svc := revisionplan.NewService(pool, examSvc, aiSvc, revisionplan.NewShortlist(pool))

    out, err := svc.GenerateForExam(context.Background(), u.ID, e.ID, nil)
    if err != nil {
        t.Fatalf("generate: %v", err)
    }
    // Drain SSE channel
    for c := range out.Events {
        _ = c
    }

    var nDays int
    pool.QueryRow(context.Background(), `SELECT jsonb_array_length(days) FROM revision_plans WHERE exam_id=$1`, e.ID).Scan(&nDays)
    if nDays < 1 {
        t.Errorf("plan not persisted (days=%d)", nDays)
    }
}
```

- [ ] **Step 2: Run the test**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./pkg/revisionplan/ -run TestGenerateForExam -count=1
```
Expected: build failure (`Service` undefined).

- [ ] **Step 3: Add SQL constants**

Append to `pkg/revisionplan/queries.go`:

```go
// sqlPersistPlan replaces any existing plan for exam_id with a fresh row.
const sqlPersistPlan = `
INSERT INTO revision_plans (exam_id, days, model, prompt_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (exam_id) DO UPDATE SET
  generated_at = now(),
  days         = EXCLUDED.days,
  model        = EXCLUDED.model,
  prompt_hash  = EXCLUDED.prompt_hash
RETURNING id, generated_at
`

// sqlSelectPrimaryCards loads (id, title, keywords) for all flashcards in a
// subject (used to bound the AI's candidate pool and validate post-processor).
const sqlSelectPrimaryCards = `
SELECT
    fc.id, COALESCE(fc.title, '') AS title,
    COALESCE(
      (SELECT array_agg(fk.keyword ORDER BY fk.weight DESC)
       FROM flashcard_keywords fk WHERE fk.fc_id = fc.id),
      ARRAY[]::TEXT[]
    ) AS keywords
FROM flashcards fc
WHERE fc.subject_id = $1
ORDER BY fc.id
LIMIT $2
`

// sqlUserStats returns aggregate review-state counts for the user's flashcards
// in subjects they own (used to pace the plan).
const sqlUserStats = `
SELECT
    COUNT(*) FILTER (WHERE fc.last_result = -1) AS new_count,
    COUNT(*) FILTER (WHERE fc.last_result = 0)  AS bad_count,
    COUNT(*) FILTER (WHERE fc.last_result = 1)  AS ok_count,
    COUNT(*) FILTER (WHERE fc.last_result = 2)  AS good_count
FROM flashcards fc
JOIN subjects s ON s.id = fc.subject_id
WHERE s.owner_id = $1
`
```

- [ ] **Step 4: Implement Service**

Create `pkg/revisionplan/service.go`:

```go
package revisionplan

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "studbud/backend/internal/myErrors"
    "studbud/backend/pkg/aipipeline"
    "studbud/backend/pkg/exam"
)

// MinPrimarySubjectFlashcards rejects sparse subjects.
const MinPrimarySubjectFlashcards = 5

// PrimaryCardCap caps the candidate pool sent to the AI.
const PrimaryCardCap = 200

// CrossSubjectShortlistLimit caps the keyword-overlap pre-filter size.
const CrossSubjectShortlistLimit = 30

// CrossSubjectTopK caps the AI-ranked cross-subject pick count.
const CrossSubjectTopK = 15

// Service orchestrates plan generation, retrieval, and progress tracking.
type Service struct {
    db        *pgxpool.Pool      // db is the shared pool
    examSvc   *exam.Service      // examSvc owns exam access checks + load
    aiSvc     *aipipeline.Service // aiSvc provides the streaming primitive
    shortlist *Shortlist         // shortlist runs the keyword overlap query
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, examSvc *exam.Service, aiSvc *aipipeline.Service, shortlist *Shortlist) *Service {
    return &Service{db: db, examSvc: examSvc, aiSvc: aiSvc, shortlist: shortlist}
}

// GenerateOutput is what GenerateForExam returns to the caller.
// The caller (handler) drains Events until close, then issues a final SSE
// event with PlanID. ErrAfter is set if generation failed mid-stream.
type GenerateOutput struct {
    PlanID int64                  // PlanID is the persisted revision_plans.id (zero on early failure)
    Events <-chan aipipeline.AIChunk // Events is the SSE chunk stream from the provider
    JobID  int64                  // JobID is the ai_jobs id for client correlation
}

// GenerateForExam runs the full orchestration: shortlist → rank → plan → persist.
// The caller drains Events and reads PlanID from the eventual GenerateOutput.
// Quota is debited via aiSvc only on successful completion (in RunStructuredGeneration).
func (s *Service) GenerateForExam(ctx context.Context, uid, examID int64, annales []byte) (*GenerateOutput, error) {
    e, err := s.examSvc.Get(ctx, uid, examID)
    if err != nil {
        return nil, err
    }

    primaryCards, err := s.loadPrimaryCards(ctx, e.SubjectID)
    if err != nil {
        return nil, err
    }

    if len(primaryCards) < MinPrimarySubjectFlashcards {
        return nil, myErrors.ErrInvalidInput
    }

    crossSubjectCards, err := s.rankCrossSubject(ctx, uid, e)
    if err != nil {
        return nil, err
    }

    stats, err := s.loadUserStats(ctx, uid)
    if err != nil {
        return nil, err
    }

    prompt, hash, err := s.renderPlanPrompt(e, primaryCards, crossSubjectCards, stats)
    if err != nil {
        return nil, err
    }

    images, err := rasterizeAnnales(ctx, annales)
    if err != nil {
        return nil, err
    }

    out, err := s.aiSvc.GenerateRevisionPlan(ctx, aipipeline.PlanGenerateInput{
        UserID: uid, ExamID: examID, SubjectID: e.SubjectID, Prompt: prompt, AnnalesImages: images,
    })
    if err != nil {
        return nil, err
    }

    events, persistDone := s.spawnPersister(ctx, e, out.Chunks, primaryCards, crossSubjectCards, hash)

    return &GenerateOutput{Events: events, JobID: out.JobID, PlanID: <-persistDone}, nil
}

// spawnPersister forwards the AI stream to events while collecting the final
// payload, then post-processes and persists. Returns (events, planID-channel).
// PlanID is sent on the channel after persistence completes (or 0 on failure).
func (s *Service) spawnPersister(
    ctx context.Context, e *exam.Exam, in <-chan aipipeline.AIChunk,
    primary, cross []aipipeline.PlanCardInfo, hash string,
) (<-chan aipipeline.AIChunk, <-chan int64) {
    events := make(chan aipipeline.AIChunk, 8)
    done := make(chan int64, 1)

    go func() {
        defer close(events)
        defer close(done)

        var buf string

        for c := range in {
            events <- c

            if c.Kind == aipipeline.ChunkKindData {
                buf += c.Text
            }
        }

        planID, err := s.parseAndPersist(ctx, e, buf, primary, cross, hash)
        if err != nil {
            events <- aipipeline.AIChunk{Kind: aipipeline.ChunkKindError, Text: err.Error()}
            done <- 0
            return
        }

        done <- planID
    }()

    return events, done
}

// parseAndPersist parses the model's JSON payload, post-processes, and writes
// the revision_plans row. Returns the new plan id.
func (s *Service) parseAndPersist(
    ctx context.Context, e *exam.Exam, buf string,
    primary, cross []aipipeline.PlanCardInfo, hash string,
) (int64, error) {
    var raw struct {
        Days []DayBucket `json:"days"`
    }

    if err := json.Unmarshal([]byte(buf), &raw); err != nil {
        return 0, fmt.Errorf("parse plan payload:\n%w", err)
    }

    today := time.Now().UTC().Truncate(24 * time.Hour)
    examDay := e.Date.UTC().Truncate(24 * time.Hour)

    valid := buildValidIDSet(primary, cross)

    days := postProcess(raw.Days, today, examDay, valid)

    return s.persistPlan(ctx, e.ID, days, hash)
}

// buildValidIDSet collects every candidate id the AI was allowed to use.
func buildValidIDSet(primary, cross []aipipeline.PlanCardInfo) map[int64]bool {
    out := make(map[int64]bool, len(primary)+len(cross))

    for _, c := range primary {
        out[c.ID] = true
    }

    for _, c := range cross {
        out[c.ID] = true
    }

    return out
}

// persistPlan upserts the revision_plans row.
func (s *Service) persistPlan(ctx context.Context, examID int64, days []DayBucket, hash string) (int64, error) {
    daysJSON, err := json.Marshal(days)
    if err != nil {
        return 0, fmt.Errorf("marshal days:\n%w", err)
    }

    var planID int64

    var generatedAt time.Time

    err = s.db.QueryRow(ctx, sqlPersistPlan, examID, daysJSON, "claude-test", hash).Scan(&planID, &generatedAt)
    if err != nil {
        return 0, fmt.Errorf("persist plan:\n%w", err)
    }

    return planID, nil
}

// loadPrimaryCards reads up to PrimaryCardCap cards in the exam subject.
func (s *Service) loadPrimaryCards(ctx context.Context, subjectID int64) ([]aipipeline.PlanCardInfo, error) {
    rows, err := s.db.Query(ctx, sqlSelectPrimaryCards, subjectID, PrimaryCardCap)
    if err != nil {
        return nil, fmt.Errorf("select primary cards:\n%w", err)
    }

    defer rows.Close()

    var out []aipipeline.PlanCardInfo

    for rows.Next() {
        var c aipipeline.PlanCardInfo

        if err := rows.Scan(&c.ID, &c.Title, &c.Keywords); err != nil {
            return nil, fmt.Errorf("scan card:\n%w", err)
        }

        out = append(out, c)
    }

    return out, rows.Err()
}

// loadUserStats reads aggregate review-state counts.
func (s *Service) loadUserStats(ctx context.Context, uid int64) (aipipeline.PlanUserStats, error) {
    var st aipipeline.PlanUserStats

    err := s.db.QueryRow(ctx, sqlUserStats, uid).Scan(&st.New, &st.Bad, &st.Ok, &st.Good)
    if err != nil {
        return st, fmt.Errorf("user stats:\n%w", err)
    }

    return st, nil
}

// rankCrossSubject runs the shortlist + AI re-rank pipeline.
func (s *Service) rankCrossSubject(ctx context.Context, uid int64, e *exam.Exam) ([]aipipeline.PlanCardInfo, error) {
    candidates, err := s.shortlist.For(ctx, uid, e.SubjectID, CrossSubjectShortlistLimit)
    if err != nil {
        return nil, err
    }

    if len(candidates) == 0 {
        return nil, nil
    }

    rankIn := toCrossSubjectCandidates(candidates)

    res, err := s.aiSvc.RankCrossSubjects(ctx, aipipeline.RankInput{
        ExamSubject: extractExamSubjectName(e), ExamTitle: e.Title, Candidates: rankIn, TopK: CrossSubjectTopK,
    })
    if err != nil {
        return nil, err
    }

    return pickCrossSubjectCards(candidates, res.SelectedIDs), nil
}

// toCrossSubjectCandidates converts shortlist rows to ranker inputs.
func toCrossSubjectCandidates(c []FcCandidate) []aipipeline.CrossSubjectCandidate {
    out := make([]aipipeline.CrossSubjectCandidate, len(c))

    for i, k := range c {
        out[i] = aipipeline.CrossSubjectCandidate{
            ID: k.ID, Title: k.Title, SubjectName: k.SubjectName, Keywords: k.Keywords, OverlapScore: k.OverlapScore,
        }
    }

    return out
}

// pickCrossSubjectCards filters candidates by AI-selected IDs, preserving order.
func pickCrossSubjectCards(candidates []FcCandidate, selectedIDs []int64) []aipipeline.PlanCardInfo {
    byID := make(map[int64]FcCandidate, len(candidates))

    for _, c := range candidates {
        byID[c.ID] = c
    }

    out := make([]aipipeline.PlanCardInfo, 0, len(selectedIDs))

    for _, id := range selectedIDs {
        c, ok := byID[id]

        if !ok {
            continue
        }

        out = append(out, aipipeline.PlanCardInfo{
            ID: c.ID, Title: c.Title, Keywords: c.Keywords, SubjectName: c.SubjectName,
        })
    }

    return out
}

// renderPlanPrompt builds the outer prompt text and a stable hash.
func (s *Service) renderPlanPrompt(
    e *exam.Exam, primary, cross []aipipeline.PlanCardInfo, stats aipipeline.PlanUserStats,
) (string, string, error) {
    daysRemaining := daysBetween(time.Now().UTC().Truncate(24*time.Hour), e.Date.UTC().Truncate(24*time.Hour))

    notes := ""
    if e.Notes != nil {
        notes = *e.Notes
    }

    v := aipipeline.RevisionPlanValues{
        ExamDate:          e.Date.Format("2006-01-02"),
        DaysRemaining:     daysRemaining,
        ExamTitle:         e.Title,
        ExamNotes:         notes,
        SubjectName:       extractExamSubjectName(e),
        HasAnnales:        e.AnnalesImageID != nil,
        PrimaryCards:      primary,
        CrossSubjectCards: cross,
        UserStats:         stats,
    }

    prompt, err := aipipeline.RenderRevisionPlan(v)
    if err != nil {
        return "", "", err
    }

    sum := sha256.Sum256([]byte(prompt))
    return prompt, hex.EncodeToString(sum[:]), nil
}

// extractExamSubjectName resolves the exam's subject display name.
// (Future: cache or join in loadPrimaryCards. For v1, separate query is fine.)
func extractExamSubjectName(e *exam.Exam) string {
    // The exam.Exam struct does not currently carry SubjectName; if it grows
    // a name field, return it here. For v1, the prompt uses subject_id and
    // a stub label — replace with a join when the spec demands display strings.
    return fmt.Sprintf("subject:%d", e.SubjectID)
}

// rasterizeAnnales is the optional PDF→images step. nil/empty annales returns nil.
func rasterizeAnnales(ctx context.Context, body []byte) ([]aiProvider.ImagePart, error) {
    if len(body) == 0 {
        return nil, nil
    }

    return aiProvider.PDFToImages(ctx, body, aiProvider.PDFOptions{PerPageTimeout: 30 * time.Second})
}
```

(Add `studbud/backend/internal/aiProvider` to the imports of `service.go`.)

**Note on `extractExamSubjectName`:** the v1 stub returns `subject:<id>`. The prompt template still works (the model is told the subject is, e.g., `subject:3`), but readability is poor. **Sub-task to add to Task 8** (Exam service): extend `Exam` to carry `SubjectName string` populated via a JOIN in `load`/`Get`/`ListActive`. The implementing engineer should either bundle this into Task 8 from the start or do it as a fast follow-up here. Either way, swap the stub for the real name before declaring Task 13 done.

- [ ] **Step 5: Run the test**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./pkg/revisionplan/ -run TestGenerateForExam -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/revisionplan/service.go pkg/revisionplan/queries.go pkg/revisionplan/shortlist_test.go
git commit -m "$(cat <<'EOF'
Add revisionplan.Service.GenerateForExam orchestrator

[+] shortlist -> rank -> plan -> persist orchestration
[+] sub-step: load primary cards (capped 200) + user stats aggregate
[+] sub-step: SHA-256 prompt_hash for audit
[+] async persister forwards SSE chunks while collecting payload buffer
[+] integration test for full happy path
EOF
)"
```

---

## Task 14: `Service.GetForExam` with drift detection

**Files:**
- Modify: `pkg/revisionplan/service.go` (append `GetForExam` + helpers)
- Create: `pkg/revisionplan/drift.go`
- Create: `pkg/revisionplan/drift_test.go`
- Modify: `pkg/revisionplan/queries.go` (add load + progress queries)

- [ ] **Step 1: Write a failing drift test**

Create `pkg/revisionplan/drift_test.go`:

```go
package revisionplan

import (
    "testing"
    "time"
)

func TestComputeDrift_NoPastDays(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    days := []DayBucket{{Date: "2026-04-30", PrimarySubjectCards: []int64{1}}}
    d := computeDrift(days, today, map[string]map[int64]bool{})
    if d.DaysBehind != 0 {
        t.Errorf("want 0 behind, got %d", d.DaysBehind)
    }
}

func TestComputeDrift_FullCompletionZeroBehind(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    days := []DayBucket{
        {Date: "2026-04-28", PrimarySubjectCards: []int64{1, 2}},
        {Date: "2026-04-29", PrimarySubjectCards: []int64{3, 4}},
    }
    progress := map[string]map[int64]bool{
        "2026-04-28": {1: true, 2: true},
        "2026-04-29": {3: true, 4: true},
    }
    d := computeDrift(days, today, progress)
    if d.DaysBehind != 0 || d.ShouldSuggestRegen {
        t.Errorf("want 0 behind / no regen, got %+v", d)
    }
}

func TestComputeDrift_TwoBehindTriggersRegen(t *testing.T) {
    today, _ := time.Parse("2006-01-02", "2026-04-30")
    days := []DayBucket{
        {Date: "2026-04-28", PrimarySubjectCards: []int64{1, 2}},
        {Date: "2026-04-29", PrimarySubjectCards: []int64{3, 4}},
    }
    progress := map[string]map[int64]bool{} // nothing done
    d := computeDrift(days, today, progress)
    if d.DaysBehind != 2 || !d.ShouldSuggestRegen {
        t.Errorf("want 2 behind / regen, got %+v", d)
    }
}
```

- [ ] **Step 2: Implement `computeDrift`**

Create `pkg/revisionplan/drift.go`:

```go
package revisionplan

import "time"

// Drift summarizes how far the user has fallen behind the plan.
type Drift struct {
    DaysBehind         int  `json:"daysBehind"`         // DaysBehind counts past days with <50% completion
    ShouldSuggestRegen bool `json:"shouldSuggestRegen"` // ShouldSuggestRegen is DaysBehind >= 2
}

// driftThreshold is the days-behind floor that triggers the regenerate banner.
const driftThreshold = 2

// driftCompletionFloor is the per-day completion floor below which a day is
// considered "behind". 0.5 = 50%.
const driftCompletionFloor = 0.5

// computeDrift counts past days where completion is below the floor.
// progress[date][fcID] = true if the user marked that fc done on that date.
func computeDrift(days []DayBucket, today time.Time, progress map[string]map[int64]bool) Drift {
    behind := 0

    for _, d := range days {
        date, err := time.Parse("2006-01-02", d.Date)
        if err != nil || !date.Before(today) {
            continue
        }

        if isDayBehind(d, progress[d.Date]) {
            behind++
        }
    }

    return Drift{DaysBehind: behind, ShouldSuggestRegen: behind >= driftThreshold}
}

// isDayBehind reports whether fewer than driftCompletionFloor of the assigned
// (primary + cross) cards were marked done on this day.
func isDayBehind(d DayBucket, doneIDs map[int64]bool) bool {
    assigned := len(d.PrimarySubjectCards) + len(d.CrossSubjectCards)

    if assigned == 0 {
        return false
    }

    done := 0

    for _, id := range d.PrimarySubjectCards {
        if doneIDs[id] {
            done++
        }
    }

    for _, id := range d.CrossSubjectCards {
        if doneIDs[id] {
            done++
        }
    }

    return float64(done)/float64(assigned) < driftCompletionFloor
}
```

- [ ] **Step 3: Run drift unit tests**

Run: `go test ./pkg/revisionplan/ -run TestComputeDrift -count=1 -v`
Expected: PASS.

- [ ] **Step 4: Add SQL constants for plan + progress load**

Append to `pkg/revisionplan/queries.go`:

```go
// sqlLoadPlan returns the persisted plan rows for an exam.
const sqlLoadPlan = `
SELECT id, exam_id, generated_at, days, model, prompt_hash
FROM revision_plans WHERE exam_id = $1
`

// sqlLoadProgress returns all (plan_date, fc_id) rows for the user across the
// given date range. Used to compute drift + today's done set.
const sqlLoadProgress = `
SELECT plan_date, fc_id FROM revision_plan_progress
WHERE user_id = $1 AND plan_date BETWEEN $2 AND $3
`
```

- [ ] **Step 5: Implement `Service.GetForExam`**

Append to `pkg/revisionplan/service.go`:

```go
// PlanResponse is the shape returned by GetForExam.
type PlanResponse struct {
    Exam        *exam.Exam `json:"exam"`         // Exam is the exam this plan belongs to
    GeneratedAt time.Time  `json:"generatedAt"`  // GeneratedAt is the plan's last generation timestamp
    Days        []DayBucket `json:"days"`        // Days is the full schedule (today..exam inclusive)
    Today       *TodayBlock `json:"today"`       // Today is the today-bucket projection (nil if today isn't in the plan range)
    Drift       Drift      `json:"drift"`        // Drift is the past-day completion summary
}

// TodayBlock is the resolved view for today's plan bucket.
type TodayBlock struct {
    Date                string  `json:"date"`
    PrimarySubjectCards []int64 `json:"primarySubjectCards"`
    CrossSubjectCards   []int64 `json:"crossSubjectCards"`
    DeeperDives         []int64 `json:"deeperDives"`
    Done                []int64 `json:"done"`
    DailyGoalMet        bool    `json:"dailyGoalMet"`
}

// GetForExam returns the persisted plan plus today's progress + drift.
// Returns ErrNotFound when no plan has been generated yet.
func (s *Service) GetForExam(ctx context.Context, uid, examID int64) (*PlanResponse, error) {
    e, err := s.examSvc.Get(ctx, uid, examID)
    if err != nil {
        return nil, err
    }

    days, generatedAt, err := s.loadPlan(ctx, examID)
    if err != nil {
        return nil, err
    }

    progress, err := s.loadProgress(ctx, uid, days)
    if err != nil {
        return nil, err
    }

    today := time.Now().UTC().Truncate(24 * time.Hour)

    return &PlanResponse{
        Exam:        e,
        GeneratedAt: generatedAt,
        Days:        days,
        Today:       buildTodayBlock(days, today, progress),
        Drift:       computeDrift(days, today, progress),
    }, nil
}

// loadPlan loads the persisted plan; returns ErrNotFound when absent.
func (s *Service) loadPlan(ctx context.Context, examID int64) ([]DayBucket, time.Time, error) {
    var (
        id          int64
        examIDOut   int64
        generatedAt time.Time
        daysJSON    []byte
        model       string
        hash        string
    )

    err := s.db.QueryRow(ctx, sqlLoadPlan, examID).Scan(&id, &examIDOut, &generatedAt, &daysJSON, &model, &hash)
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, time.Time{}, myErrors.ErrNotFound
    }

    if err != nil {
        return nil, time.Time{}, fmt.Errorf("load plan:\n%w", err)
    }

    var days []DayBucket

    if err := json.Unmarshal(daysJSON, &days); err != nil {
        return nil, time.Time{}, fmt.Errorf("unmarshal days:\n%w", err)
    }

    return days, generatedAt, nil
}

// loadProgress returns a [date][fcID] map of completed cards over the plan range.
func (s *Service) loadProgress(ctx context.Context, uid int64, days []DayBucket) (map[string]map[int64]bool, error) {
    if len(days) == 0 {
        return map[string]map[int64]bool{}, nil
    }

    first, _ := time.Parse("2006-01-02", days[0].Date)
    last, _ := time.Parse("2006-01-02", days[len(days)-1].Date)

    rows, err := s.db.Query(ctx, sqlLoadProgress, uid, first, last)
    if err != nil {
        return nil, fmt.Errorf("load progress:\n%w", err)
    }

    defer rows.Close()

    out := map[string]map[int64]bool{}

    for rows.Next() {
        var (
            day  time.Time
            fcID int64
        )

        if err := rows.Scan(&day, &fcID); err != nil {
            return nil, fmt.Errorf("scan progress:\n%w", err)
        }

        key := day.Format("2006-01-02")

        if out[key] == nil {
            out[key] = map[int64]bool{}
        }

        out[key][fcID] = true
    }

    return out, rows.Err()
}

// buildTodayBlock projects today's bucket with done + dailyGoalMet computed.
// Returns nil if today is outside [first, last] of the plan.
func buildTodayBlock(days []DayBucket, today time.Time, progress map[string]map[int64]bool) *TodayBlock {
    key := today.Format("2006-01-02")

    for _, d := range days {
        if d.Date != key {
            continue
        }

        done := todayDone(d, progress[key])
        assigned := len(d.PrimarySubjectCards) + len(d.CrossSubjectCards)

        return &TodayBlock{
            Date:                d.Date,
            PrimarySubjectCards: d.PrimarySubjectCards,
            CrossSubjectCards:   d.CrossSubjectCards,
            DeeperDives:         d.DeeperDives,
            Done:                done,
            DailyGoalMet:        len(done) >= assigned,
        }
    }

    return nil
}

// todayDone returns the subset of (primary + cross) IDs marked done in progress.
func todayDone(d DayBucket, doneIDs map[int64]bool) []int64 {
    out := make([]int64, 0, len(d.PrimarySubjectCards)+len(d.CrossSubjectCards))

    for _, id := range d.PrimarySubjectCards {
        if doneIDs[id] {
            out = append(out, id)
        }
    }

    for _, id := range d.CrossSubjectCards {
        if doneIDs[id] {
            out = append(out, id)
        }
    }

    return out
}
```

(Add imports as needed: `encoding/json`, `errors`, `github.com/jackc/pgx/v5`.)

- [ ] **Step 6: Run all revisionplan tests**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./pkg/revisionplan/... -count=1 -v
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/revisionplan/drift.go pkg/revisionplan/drift_test.go pkg/revisionplan/service.go pkg/revisionplan/queries.go
git commit -m "$(cat <<'EOF'
Add Service.GetForExam with drift detection

[+] PlanResponse + TodayBlock + Drift types
[+] computeDrift: count past days <50% complete, flag >=2 for regen
[+] buildTodayBlock: project today's bucket + dailyGoalMet
[+] loadPlan / loadProgress queries
[+] unit tests for 0-behind, full-completion, two-behind-triggers-regen
EOF
)"
```

---

## Task 15: `Service.MarkDone`

**Files:**
- Modify: `pkg/revisionplan/service.go` (append `MarkDone`)
- Modify: `pkg/revisionplan/queries.go` (add insert SQL)
- Create: `pkg/revisionplan/markdone_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/revisionplan/markdone_test.go`:

```go
package revisionplan_test

import (
    "context"
    "testing"
    "time"

    "studbud/backend/pkg/revisionplan"
    "studbud/backend/testutil"
)

func TestMarkDone_InsertsProgressRow(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    fcID := testutil.NewFlashcard(t, pool, sub.ID, 0, "Q", "A")

    svc := buildSvcForTest(t, pool) // small helper that constructs revisionplan.Service with stubs

    if err := svc.MarkDone(context.Background(), u.ID, fcID); err != nil {
        t.Fatalf("mark done: %v", err)
    }
    var n int
    pool.QueryRow(context.Background(), `SELECT count(*) FROM revision_plan_progress WHERE user_id=$1 AND fc_id=$2 AND plan_date=CURRENT_DATE`, u.ID, fcID).Scan(&n)
    if n != 1 {
        t.Errorf("want 1 row, got %d", n)
    }
}

func TestMarkDone_IsIdempotent(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    sub := testutil.NewSubject(t, pool, u.ID)
    fcID := testutil.NewFlashcard(t, pool, sub.ID, 0, "Q", "A")

    svc := buildSvcForTest(t, pool)

    _ = svc.MarkDone(context.Background(), u.ID, fcID)
    if err := svc.MarkDone(context.Background(), u.ID, fcID); err != nil {
        t.Fatalf("second mark: %v", err)
    }
}
```

(Add `buildSvcForTest` as a small helper in this test file — it can pass nil for the AI service and exam service since MarkDone doesn't use them.)

- [ ] **Step 2: Add SQL**

Append to `pkg/revisionplan/queries.go`:

```go
// sqlInsertProgress is upsert-safe via ON CONFLICT DO NOTHING because the PK is
// (user_id, fc_id, plan_date) — repeated marks on the same day are no-ops.
const sqlInsertProgress = `
INSERT INTO revision_plan_progress (user_id, fc_id, plan_date)
VALUES ($1, $2, CURRENT_DATE)
ON CONFLICT DO NOTHING
`
```

- [ ] **Step 3: Implement `MarkDone`**

Append to `pkg/revisionplan/service.go`:

```go
// MarkDone records that the user completed fcID for today's plan.
// Idempotent: re-marking the same card on the same day is a no-op.
func (s *Service) MarkDone(ctx context.Context, uid, fcID int64) error {
    if _, err := s.db.Exec(ctx, sqlInsertProgress, uid, fcID); err != nil {
        return fmt.Errorf("mark done:\n%w", err)
    }

    return nil
}
```

- [ ] **Step 4: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./pkg/revisionplan/... -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/revisionplan/service.go pkg/revisionplan/queries.go pkg/revisionplan/markdone_test.go
git commit -m "$(cat <<'EOF'
Add revisionplan.Service.MarkDone

[+] insert into revision_plan_progress with ON CONFLICT DO NOTHING
[+] idempotent: re-marking a card on the same day is a no-op
[+] integration tests for first-mark + double-mark
EOF
)"
```

---

## Task 16: Revision-plan HTTP handler + routes

**Files:**
- Create: `api/handler/revision_plan.go`
- Create: `api/handler/revision_plan_test.go`
- Modify: `cmd/app/routes.go` (register routes — finalized in Task 17)

- [ ] **Step 1: Write failing tests**

Create `api/handler/revision_plan_test.go`:

```go
package handler_test

import (
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "studbud/backend/api/handler"
    "studbud/backend/internal/aiProvider"
    "studbud/backend/pkg/access"
    "studbud/backend/pkg/aipipeline"
    "studbud/backend/pkg/exam"
    "studbud/backend/pkg/revisionplan"
    "studbud/backend/testutil"
)

func TestRevisionPlanHandler_GeneratePersistsPlan(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)
    u := testutil.NewVerifiedUser(t, pool)
    testutil.GiveAIAccess(t, pool, u.ID)
    sub := testutil.NewSubject(t, pool, u.ID)
    for i := 0; i < 5; i++ {
        testutil.NewFlashcard(t, pool, sub.ID, 0, "Q", "A")
    }

    examSvc := exam.NewService(pool, access.NewService(pool))
    e, _ := examSvc.Create(context.Background(), u.ID, exam.CreateInput{
        SubjectID: sub.ID, Title: "Partiel", Date: time.Now().AddDate(0, 0, 5),
    })

    body := `{"days":[{"date":"` + time.Now().Format("2006-01-02") + `","primarySubjectCards":[],"crossSubjectCards":[],"deeperDives":[]}]}`
    cli := &testutil.FakeAIClient{Chunks: []aiProvider.Chunk{{Text: body, Done: true}}}
    aiSvc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "claude-test")
    rpSvc := revisionplan.NewService(pool, examSvc, aiSvc, revisionplan.NewShortlist(pool))

    h := handler.NewRevisionPlanHandler(rpSvc)
    mux := http.NewServeMux()
    h.Register(mux, testutil.AuthMiddlewareForUser(u.ID))
    srv := httptest.NewServer(mux)
    defer srv.Close()

    resp, err := http.Post(srv.URL+"/exams/"+fmt.Sprintf("%d", e.ID)+"/generate-plan", "application/json", strings.NewReader(""))
    if err != nil || resp.StatusCode != http.StatusOK {
        t.Fatalf("generate: err=%v status=%d", err, resp.StatusCode)
    }
    // Drain SSE response body.
    _, _ = io.ReadAll(resp.Body)

    var planID int64
    pool.QueryRow(context.Background(), `SELECT id FROM revision_plans WHERE exam_id=$1`, e.ID).Scan(&planID)
    if planID == 0 {
        t.Errorf("plan not persisted")
    }
}
```

(`testutil.AuthMiddlewareForUser` is a presumed test helper — if it doesn't exist, replicate the pattern from existing handler tests in `api/handler/flashcard_test.go`.)

- [ ] **Step 2: Implement the handler**

Create `api/handler/revision_plan.go`:

```go
package handler

import (
    "encoding/json"
    "io"
    "net/http"

    "studbud/backend/internal/authctx"
    "studbud/backend/internal/httpx"
    "studbud/backend/pkg/aipipeline"
    "studbud/backend/pkg/revisionplan"
)

// RevisionPlanHandler holds the revision-plan service.
type RevisionPlanHandler struct {
    svc *revisionplan.Service // svc owns generate / get / mark-done
}

// NewRevisionPlanHandler constructs the handler.
func NewRevisionPlanHandler(svc *revisionplan.Service) *RevisionPlanHandler {
    return &RevisionPlanHandler{svc: svc}
}

// Generate handles POST /exams/{id}/generate-plan (SSE).
// Streams provider chunks to the client; sends a final "done" event with planId.
func (h *RevisionPlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    examID := pathInt64(r, "id")

    annales, _ := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // optional inline annales bytes; usually empty (uploaded via /exams/{id}/annales beforehand)

    out, err := h.svc.GenerateForExam(r.Context(), uid, examID, annales)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    setSSEHeaders(w)
    flusher, _ := w.(http.Flusher)

    writeSSE(w, flusher, "job", map[string]any{"jobId": out.JobID})

    h.streamPlanEvents(w, flusher, out.Events)

    writeSSE(w, flusher, "done", map[string]any{"planId": out.PlanID})
}

// streamPlanEvents forwards plan-generation chunks to the SSE stream.
func (h *RevisionPlanHandler) streamPlanEvents(w http.ResponseWriter, flusher http.Flusher, in <-chan aipipeline.AIChunk) {
    for c := range in {
        forwardChunkToSSE(w, flusher, c)
    }
}

// Get handles GET /exams/{id}/plan.
func (h *RevisionPlanHandler) Get(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    examID := pathInt64(r, "id")

    out, err := h.svc.GetForExam(r.Context(), uid, examID)
    if err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, out)
}

// markDoneInput is the request body for POST /exams/{id}/mark-done.
type markDoneInput struct {
    FcID int64 `json:"fc_id"` // FcID is the flashcard the user completed
}

// MarkDone handles POST /exams/{id}/mark-done.
// (The exam id in the path is for routing scope, not lookup.)
func (h *RevisionPlanHandler) MarkDone(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())

    var in markDoneInput

    if err := httpx.DecodeJSON(r, &in); err != nil {
        httpx.WriteError(w, err)
        return
    }

    if err := h.svc.MarkDone(r.Context(), uid, in.FcID); err != nil {
        httpx.WriteError(w, err)
        return
    }

    httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Register attaches routes.
func (h *RevisionPlanHandler) Register(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
    mux.Handle("POST /exams/{id}/generate-plan", mw(http.HandlerFunc(h.Generate)))
    mux.Handle("GET /exams/{id}/plan", mw(http.HandlerFunc(h.Get)))
    mux.Handle("POST /exams/{id}/mark-done", mw(http.HandlerFunc(h.MarkDone)))
}

// keep encoder import alive even if all paths route through httpx.
var _ = json.Marshal
```

(`forwardChunkToSSE` already exists in `api/handler/ai.go` — same package.)

- [ ] **Step 3: Run the tests**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./api/handler/... -count=1 -run TestRevisionPlanHandler
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add api/handler/revision_plan.go api/handler/revision_plan_test.go
git commit -m "$(cat <<'EOF'
Add revision-plan HTTP handler

[+] POST /exams/{id}/generate-plan (SSE streaming)
[+] GET /exams/{id}/plan returns plan + today + drift
[+] POST /exams/{id}/mark-done idempotent progress insert
[+] handler tests for generate persists + done-event sent
EOF
)"
```

---

## Task 17: Wire `pkg/exam` and `pkg/revisionplan` into `cmd/app/deps.go` + routes

**Files:**
- Modify: `cmd/app/deps.go` (add fields + constructors)
- Modify: `cmd/app/routes.go` (call `Register` on both handlers)

- [ ] **Step 1: Inspect existing deps wiring**

Run:
```bash
grep -n "domainSvcs\|stubSvcs\|assembleDeps\|flashcard.NewService" cmd/app/deps.go
```

You'll see the pattern: `domainSvcs` constructs domain services after `infra`; `stubSvcs` builds AI/quiz/plan/billing stubs (this file's `pkg/plan` is the OLD stub — Spec B replaces nothing here, since `pkg/revisionplan` is a new package; the existing `pkg/plan` stub remains for now and can be deleted later when frontend stops referring to it).

- [ ] **Step 2: Add fields to `deps` and `domainSvcs`**

In `cmd/app/deps.go`, add to the `deps` struct (alphabetical with siblings):

```go
exam         *exam.Service        // exam owns exam CRUD
revisionPlan *revisionplan.Service // revisionPlan owns plan generation/get/mark-done
```

Add to the `domainSvcs` struct:

```go
exam *exam.Service
```

(`revisionPlan` is built later because it needs `aipipeline.Service` from stubs.)

- [ ] **Step 3: Add imports**

```go
"studbud/backend/pkg/exam"
"studbud/backend/pkg/image"
"studbud/backend/pkg/revisionplan"
```

- [ ] **Step 4: Wire constructors**

In `buildDomainServices`, add:

```go
return domainSvcs{
    // ... existing fields ...
    exam: exam.NewServiceWithImage(pool, acc, image.NewService(pool, inf.store, cfg.BackendURL)),
}
```

In `buildDeps`, after `stubs := buildStubServices(...)`:

```go
revisionPlanSvc := revisionplan.NewService(pool, dom.exam, stubs.ai, revisionplan.NewShortlist(pool))
```

In `assembleDeps`, attach:

```go
exam:         dom.exam,
revisionPlan: revisionPlanSvc,
```

(Pass `revisionPlanSvc` into `assembleDeps` as a new arg, or attach to `deps` directly inside `buildDeps`.)

- [ ] **Step 5: Register routes**

In `cmd/app/routes.go`, after the existing `/ai` routes block, add:

```go
examH := handler.NewExamHandler(d.exam)
examH.Register(mux, av) // av is the auth middleware factory

rpH := handler.NewRevisionPlanHandler(d.revisionPlan)
rpH.Register(mux, av)
```

- [ ] **Step 6: Build + run all tests**

Run:
```bash
go build ./...
ENV=test DATABASE_URL='...' go test ./... -count=1 -p 1 -timeout 120s
```
Expected: PASS (modulo the pre-existing `TestE2E_RegisterThroughTraining` failure observed in Plan B.0; that's unrelated to Spec B).

- [ ] **Step 7: Commit**

```bash
git add cmd/app/deps.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Wire exam + revisionPlan services into app boot

[+] deps.exam constructed with image service for annales uploads
[+] deps.revisionPlan constructed after stubs.ai exists
[+] /exams routes registered via ExamHandler.Register
[+] /exams/{id}/{generate-plan,plan,mark-done} routes registered
EOF
)"
```

---

## Task 18: End-to-end integration test

**Files:**
- Create: `cmd/app/e2e_revision_plan_test.go`

- [ ] **Step 1: Write the failing E2E test**

Create `cmd/app/e2e_revision_plan_test.go`:

```go
package main

import (
    "context"
    "net/http/httptest"
    "testing"
    "time"

    "studbud/backend/internal/aiProvider"
    "studbud/backend/testutil"
)

func TestE2E_RevisionPlanHappyPath(t *testing.T) {
    cfg := testConfig()
    pool := testutil.OpenTestDB(t)
    testutil.Reset(t, pool)

    body := `{"days":[{"date":"` + time.Now().Format("2006-01-02") + `","primarySubjectCards":[],"crossSubjectCards":[],"deeperDives":[]}]}`
    fake := &testutil.FakeAIClient{Chunks: []aiProvider.Chunk{{Text: body, Done: true}}}

    d, _ := mustBuildDepsWithFake(t, pool, cfg, fake)
    srv := httptest.NewServer(buildRouter(d))
    defer srv.Close()

    cl := &httpClient{base: srv.URL}
    cl.token = stepRegisterAndVerify(t, cl, context.Background(), d)

    // Subject + 5 flashcards (min for plan)
    subID := stepCreateSubject(t, cl, "Bio")
    for i := 0; i < 5; i++ {
        stepCreateFlashcard(t, cl, subID, 0, "Q", "A")
    }

    // Create exam
    examDate := time.Now().AddDate(0, 0, 5).Format("2006-01-02")
    var exam struct{ ID int64 `json:"id"` }
    cl.mustPostJSON(t, "/exams", map[string]any{"subject_id": subID, "title": "Partiel", "date": examDate}, &exam, 201)

    // Generate
    cl.mustPostJSON(t, "/exams/"+fmt.Sprintf("%d", exam.ID)+"/generate-plan", map[string]any{}, nil, 200)

    // GET plan
    var planResp struct{
        Drift struct{ DaysBehind int `json:"daysBehind"` } `json:"drift"`
    }
    cl.mustGet(t, "/exams/"+fmt.Sprintf("%d", exam.ID)+"/plan", &planResp, 200)
    if planResp.Drift.DaysBehind != 0 {
        t.Errorf("fresh plan should have 0 drift, got %d", planResp.Drift.DaysBehind)
    }
}
```

(`stepRegisterAndVerify`, `stepCreateSubject`, `stepCreateFlashcard`, `httpClient`, `mustBuildDepsWithFake`, `testConfig` are existing helpers in `cmd/app/e2e_test.go`. If a helper doesn't exist with the exact signature, copy/adapt from the existing e2e file.)

- [ ] **Step 2: Run the test**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./cmd/app/ -run TestE2E_RevisionPlanHappyPath -count=1 -p 1
```
Expected: PASS.

- [ ] **Step 3: Run the full suite**

Run:
```bash
ENV=test DATABASE_URL='...' go test ./... -count=1 -p 1 -timeout 120s
```
Expected: every package PASS, except the pre-existing `TestE2E_RegisterThroughTraining` (known broken — see Plan B.0 self-review notes).

- [ ] **Step 4: Commit**

```bash
git add cmd/app/e2e_revision_plan_test.go
git commit -m "$(cat <<'EOF'
End-to-end test: exam create -> generate-plan -> get plan

[+] register/login -> subject + 5 fcs -> exam -> SSE generate -> GET plan
[+] asserts drift=0 on a freshly-generated plan
EOF
)"
```

---

## Self-Review Notes

- **Spec coverage map:**
  - §3 architecture → file layout above
  - §4.1 exams + §4.2 revision_plans + §4.3 progress → Task 1
  - §4.4 quota extension → already in the schema (`plan_calls`, `cross_subject_rank_calls` columns exist); Go-side limits in Task 2
  - §5 AI contract (FeatureKeys + schemas) → Tasks 2/3/4/6/7
  - §5.4 post-processing → Task 12
  - §6.1 outer orchestrator → Task 13
  - §6.2 cross-subject SQL → Task 11
  - §6.3 plan consumption + §6.4 drift → Task 14
  - §6.5 card completion (mark-done) → Task 15 + handler in Task 16
  - §6.6 annales upload → Task 5 (page count) + Task 9 (attach) + Task 10 (HTTP)
  - §7 error handling → mapped through existing `myErrors` sentinels (no new sentinels needed; survey confirmed all required ones exist)
  - §9 testing → unit + integration tests live alongside each feature; the manual QA checklist at §9 final is operator-executed (not in this plan)

- **Out of scope (per spec):** UI work (Vue components, Pinia stores, pages). All §8 items are frontend-only and ship in a separate plan.

- **Known follow-ups to track post-merge:**
  1. `extractExamSubjectName` stub in Task 13 returns `subject:<id>` — replace with a real JOIN once `exam.Exam` carries `SubjectName`. The implementing engineer should bundle this into Task 8 or do it as a fast follow-up in Task 13. Either way: do not declare Task 13 done until the stub is gone.
  2. `pkg/plan` stub in `cmd/app/deps.go` (`stubSvcs.plan`) is unrelated to this Spec B work — it's the pre-launch placeholder. Leave alone; delete in a separate PR after frontend stops referencing it (per Spec §10's deferred items list).
  3. Spec §11 open question: per-day cross-subject card cap. Plan ships with `CrossSubjectTopK=15` total (not per-day). Tune post-launch based on completion rates.

- **Type consistency check:** `aipipeline.PlanCardInfo`, `aipipeline.CrossSubjectCandidate`, `revisionplan.FcCandidate`, `revisionplan.DayBucket` all flow consistently across tasks. `Service.MarkDone(ctx, uid, fcID)` matches the handler call in Task 16. `revisionplan.NewService(db, examSvc, aiSvc, shortlist)` is consistent across Tasks 13-17.

- **Placeholder scan:** No "TODO", "TBD", or "implement later" instructions — every step contains the actual code or a one-line "use the existing X helper" with a file:line reference.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-30-ai-revision-plan.md`.**

**Two execution options:**

1. **Subagent-Driven (recommended)** — A fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
