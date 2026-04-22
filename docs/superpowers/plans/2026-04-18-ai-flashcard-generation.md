# AI Flashcard Generation & Check — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship subscription-gated AI flashcard generation (prompt + PDF) and an AI-check button in the flashcard editor, behind a generic backend-proxied AI pipeline that Spec B (revision plan) can reuse unchanged.

**Architecture:** One generic Go pipeline primitive (`RunStructuredGeneration`) owns entitlement / quota / retry / SSE / schema validation. Three thin handlers call it: `/ai/generate-flashcards` (SSE), `/ai/check-flashcard` (JSON), plus `/ai/commit-generation` (no AI call, writes the edited draft) and `/ai/quota`. Frontend adds two routed pages (generate → review) and a modal; a single Pinia store carries state between them.

**Tech Stack:**
- Backend: Go 1.23.4, SQLite (via `mattn/go-sqlite3`), Anthropic Claude for LLM, `go-fitz` for PDF → image.
- Frontend: Vue 3 + Pinia + Vite + TypeScript, SSE via `fetch` + `ReadableStream`, existing `MarkdownToolbar` / `MarkdownPreview` components for inline editing.

**Repo layout:**
- Backend: `/Users/martonroux/Documents/WEB/study_buddy_backend/` (separate repo — `github.com/martonroux/go-study-buddy`).
- Frontend: `/Users/martonroux/Documents/WEB/studbud_3/studbud/` (this repo).
- Commit each task to the repo it touches.

**Design doc:** `docs/superpowers/specs/2026-04-18-ai-flashcard-generation-design.md` (in the frontend repo).

**Conventions:**
- One commit per completed task, message `feat(ai): <short summary>` or `test(ai): …` or `refactor(ai): …`.
- Each code step shows the code. Each test step shows the test. Each run step shows the command and expected output.
- Tasks assume a fresh subagent with no prior context — every task states the files involved and the imports needed.

---

## Pre-flight: backend test scaffolding (bootstrap once)

The existing backend has no `_test.go` files. We need a tiny test helper before we can TDD anything else.

### Task 0.1: Create backend test helper

**Files:**
- Create: `testutil/testdb.go`
- Create: `testutil/fixtures.go`

- [ ] **Step 1: Create `testutil/testdb.go`**

```go
package testutil

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// NewTestDB returns an in-memory SQLite DB with all schema created.
// The database is fresh for each call. Close it with t.Cleanup.
func NewTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ApplySchema(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}
```

- [ ] **Step 2: Create `testutil/fixtures.go`**

```go
package testutil

import (
	"database/sql"
	"testing"
	"time"
)

// CreateUser inserts a user row and returns its id.
// active = ai_subscription_active value.
func CreateUser(t *testing.T, db *sql.DB, id, username string, active bool) string {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users (id, username, email, email_verified, ai_subscription_active, created_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		id, username, username+"@example.com", active, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// CreateSubject inserts an owned subject and returns the id.
func CreateSubject(t *testing.T, db *sql.DB, ownerID, name string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO subjects (owner_id, name, color, tags, last_used)
		 VALUES (?, ?, '#ffffff', '', ?)`,
		ownerID, name, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert subject: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}
```

- [ ] **Step 3: Add the `ApplySchema` stub referenced above**

Open `db_sql/setup.go`. Export a function `ApplySchema(db *sql.DB) error` that the real app's startup and `testutil` both call. (The existing setup code likely already has similar logic — extract it or wrap it.) This plan assumes `ApplySchema` exists and is called by `testutil`. If it does not exist yet, create it as the first step of Task 1.1.

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add testutil/ db_sql/
git commit -m "test: add minimal test harness (in-memory sqlite + fixtures)"
```

---

## Phase 1 — Database schema

### Task 1.1: Add `ai_subscription_active` column + new tables

**Files:**
- Modify: `db_sql/setup.go`

- [ ] **Step 1: Inspect current schema setup**

Open `db_sql/setup.go` and identify the function that creates tables (likely one big `CREATE TABLE` block or a slice of statements). Our new statements append to the same list so `ApplySchema` creates them for fresh DBs.

- [ ] **Step 2: Add the `users.ai_subscription_active` column**

Inside the same function (after the existing `users` `CREATE TABLE`), add:

```go
// AI entitlement — idempotent column add for existing DBs.
_, _ = db.Exec(`ALTER TABLE users ADD COLUMN ai_subscription_active INTEGER NOT NULL DEFAULT 0`)
```

The `_, _ =` intentionally ignores the "duplicate column" error SQLite throws on re-run. (SQLite does not support `ADD COLUMN IF NOT EXISTS`.) If the rest of the file uses a strict error-returning pattern, wrap this in `addColumnIfMissing(db, "users", "ai_subscription_active", "INTEGER NOT NULL DEFAULT 0")` — implement that helper inline if not already present.

- [ ] **Step 3: Add `ai_jobs` table**

```go
if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS ai_jobs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          TEXT    NOT NULL,
    feature          TEXT    NOT NULL,
    status           TEXT    NOT NULL,
    subject_id       INTEGER NULL,
    flashcard_id     INTEGER NULL,
    request_params   TEXT    NOT NULL,
    input_hash       TEXT    NOT NULL,
    pdf_page_count   INTEGER NOT NULL DEFAULT 0,
    items_emitted    INTEGER NOT NULL DEFAULT 0,
    items_dropped    INTEGER NOT NULL DEFAULT 0,
    provider         TEXT    NOT NULL,
    provider_req_id  TEXT    NULL,
    input_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens    INTEGER NOT NULL DEFAULT 0,
    cost_cents       INTEGER NOT NULL DEFAULT 0,
    error_kind       TEXT    NULL,
    error_message    TEXT    NULL,
    started_at       TEXT    NOT NULL,
    finished_at      TEXT    NULL,
    FOREIGN KEY (user_id)      REFERENCES users(id),
    FOREIGN KEY (subject_id)   REFERENCES subjects(id) ON DELETE SET NULL,
    FOREIGN KEY (flashcard_id) REFERENCES flashcards(id) ON DELETE SET NULL
)`); err != nil {
    return err
}
if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS ai_jobs_user_started_idx ON ai_jobs (user_id, started_at DESC)`); err != nil {
    return err
}
if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS ai_jobs_running_idx ON ai_jobs (status) WHERE status = 'running'`); err != nil {
    return err
}
```

- [ ] **Step 4: Add `ai_quota_daily` table**

```go
if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS ai_quota_daily (
    user_id       TEXT    NOT NULL,
    date          TEXT    NOT NULL,
    prompt_calls  INTEGER NOT NULL DEFAULT 0,
    pdf_calls     INTEGER NOT NULL DEFAULT 0,
    pdf_pages     INTEGER NOT NULL DEFAULT 0,
    check_calls   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, date),
    FOREIGN KEY (user_id) REFERENCES users(id)
)`); err != nil {
    return err
}
```

- [ ] **Step 5: Run the app once to verify migrations apply cleanly**

Run: `go run ./cmd/app` (Ctrl-C after it prints the listen line).
Expected: server starts without schema errors.

- [ ] **Step 6: Commit**

```bash
git add db_sql/setup.go
git commit -m "feat(ai): add ai_jobs + ai_quota_daily tables, users.ai_subscription_active"
```

---

## Phase 2 — Quota service

### Task 2.1: Test the quota service

**Files:**
- Create: `api/service/aiQuotaService_test.go`

- [ ] **Step 1: Write the test file**

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/martonroux/go-study-buddy/testutil"
)

func TestQuotaService_AllowsFirstCall(t *testing.T) {
	db := testutil.NewTestDB(t)
	userID := testutil.CreateUser(t, db, "u1", "alice", true)
	q := NewAIQuotaService(db, DefaultQuotaLimits(), func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	})

	if err := q.Check(context.Background(), userID, FeaturePrompt, 0); err != nil {
		t.Fatalf("first call rejected: %v", err)
	}
}

func TestQuotaService_RejectsAfterLimit(t *testing.T) {
	db := testutil.NewTestDB(t)
	userID := testutil.CreateUser(t, db, "u1", "alice", true)
	lim := DefaultQuotaLimits()
	lim.PromptCalls = 2
	q := NewAIQuotaService(db, lim, func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := q.Check(ctx, userID, FeaturePrompt, 0); err != nil {
			t.Fatalf("call %d rejected: %v", i, err)
		}
		if err := q.Debit(ctx, userID, FeaturePrompt, 1, 0); err != nil {
			t.Fatalf("debit %d failed: %v", i, err)
		}
	}
	err := q.Check(ctx, userID, FeaturePrompt, 0)
	if err == nil {
		t.Fatal("expected quota_exceeded, got nil")
	}
	qErr, ok := err.(*QuotaError)
	if !ok || qErr.Kind != "quota_exceeded" {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestQuotaService_PDFPagesSeparateFromCalls(t *testing.T) {
	db := testutil.NewTestDB(t)
	userID := testutil.CreateUser(t, db, "u1", "alice", true)
	lim := DefaultQuotaLimits()
	lim.PDFCalls = 5
	lim.PDFPages = 10
	q := NewAIQuotaService(db, lim, func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	})
	ctx := context.Background()

	if err := q.Check(ctx, userID, FeaturePDF, 8); err != nil {
		t.Fatalf("first pdf rejected: %v", err)
	}
	if err := q.Debit(ctx, userID, FeaturePDF, 1, 8); err != nil {
		t.Fatalf("debit: %v", err)
	}
	// 8/10 pages used, one more 5-page pdf would exceed pages
	if err := q.Check(ctx, userID, FeaturePDF, 5); err == nil {
		t.Fatal("expected pages-quota error, got nil")
	}
	// But a 2-page pdf should still fit
	if err := q.Check(ctx, userID, FeaturePDF, 2); err != nil {
		t.Fatalf("2-page pdf wrongly rejected: %v", err)
	}
}

func TestQuotaService_DayRollover(t *testing.T) {
	db := testutil.NewTestDB(t)
	userID := testutil.CreateUser(t, db, "u1", "alice", true)
	lim := DefaultQuotaLimits()
	lim.PromptCalls = 1
	now := time.Date(2026, 4, 18, 23, 0, 0, 0, time.UTC)
	q := NewAIQuotaService(db, lim, func() time.Time { return now })
	ctx := context.Background()

	_ = q.Check(ctx, userID, FeaturePrompt, 0)
	_ = q.Debit(ctx, userID, FeaturePrompt, 1, 0)
	if err := q.Check(ctx, userID, FeaturePrompt, 0); err == nil {
		t.Fatal("expected quota_exceeded before rollover")
	}

	// Next day
	now = time.Date(2026, 4, 19, 0, 1, 0, 0, time.UTC)
	if err := q.Check(ctx, userID, FeaturePrompt, 0); err != nil {
		t.Fatalf("next day rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `go test ./api/service/ -run TestQuotaService_ -v`
Expected: FAIL — `NewAIQuotaService`, `FeaturePrompt`, `FeaturePDF`, `DefaultQuotaLimits`, `QuotaError` all undefined.

- [ ] **Step 3: Commit the failing tests**

```bash
git add api/service/aiQuotaService_test.go
git commit -m "test(ai): failing tests for quota service"
```

### Task 2.2: Implement the quota service

**Files:**
- Create: `api/service/aiQuotaService.go`
- Create: `pkg/ai/feature.go`

- [ ] **Step 1: Define the feature enum**

Create `pkg/ai/feature.go`:

```go
package ai

type FeatureKey string

const (
	FeaturePrompt FeatureKey = "generate_prompt"
	FeaturePDF    FeatureKey = "generate_pdf"
	FeatureCheck  FeatureKey = "check_flashcard"
)
```

- [ ] **Step 2: Implement the service**

Create `api/service/aiQuotaService.go`:

```go
package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/martonroux/go-study-buddy/pkg/ai"
)

// Re-exported so tests in this package can use short names.
const (
	FeaturePrompt = ai.FeaturePrompt
	FeaturePDF    = ai.FeaturePDF
	FeatureCheck  = ai.FeatureCheck
)

type QuotaLimits struct {
	PromptCalls int
	PDFCalls    int
	PDFPages    int
	CheckCalls  int
}

func DefaultQuotaLimits() QuotaLimits {
	return QuotaLimits{
		PromptCalls: 20,
		PDFCalls:    5,
		PDFPages:    100,
		CheckCalls:  50,
	}
}

type QuotaError struct {
	Kind    string // always "quota_exceeded"
	ResetAt time.Time
	Detail  string // human-readable: "prompt_calls", "pdf_pages", etc.
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("quota exceeded: %s (resets %s)", e.Detail, e.ResetAt.Format(time.RFC3339))
}

type AIQuotaService struct {
	db     *sql.DB
	limits QuotaLimits
	now    func() time.Time
}

func NewAIQuotaService(db *sql.DB, limits QuotaLimits, now func() time.Time) *AIQuotaService {
	if now == nil {
		now = time.Now
	}
	return &AIQuotaService{db: db, limits: limits, now: now}
}

func dayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

func nextMidnight(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
}

func (q *AIQuotaService) loadOrCreate(ctx context.Context, userID, date string) (prompt, pdf, pages, check int, err error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT prompt_calls, pdf_calls, pdf_pages, check_calls
		 FROM ai_quota_daily WHERE user_id=? AND date=?`, userID, date)
	err = row.Scan(&prompt, &pdf, &pages, &check)
	if err == sql.ErrNoRows {
		_, err = q.db.ExecContext(ctx,
			`INSERT INTO ai_quota_daily (user_id, date) VALUES (?, ?)`, userID, date)
		return 0, 0, 0, 0, err
	}
	return
}

// Check does not mutate state. Debit happens only on successful item emission.
// pages is 0 for non-PDF features.
func (q *AIQuotaService) Check(ctx context.Context, userID string, feat ai.FeatureKey, pages int) error {
	now := q.now()
	date := dayKey(now)
	prompt, pdf, p, check, err := q.loadOrCreate(ctx, userID, date)
	if err != nil {
		return err
	}
	reset := nextMidnight(now)
	switch feat {
	case ai.FeaturePrompt:
		if prompt >= q.limits.PromptCalls {
			return &QuotaError{Kind: "quota_exceeded", ResetAt: reset, Detail: "prompt_calls"}
		}
	case ai.FeaturePDF:
		if pdf >= q.limits.PDFCalls {
			return &QuotaError{Kind: "quota_exceeded", ResetAt: reset, Detail: "pdf_calls"}
		}
		if p+pages > q.limits.PDFPages {
			return &QuotaError{Kind: "quota_exceeded", ResetAt: reset, Detail: "pdf_pages"}
		}
	case ai.FeatureCheck:
		if check >= q.limits.CheckCalls {
			return &QuotaError{Kind: "quota_exceeded", ResetAt: reset, Detail: "check_calls"}
		}
	default:
		return fmt.Errorf("unknown feature: %s", feat)
	}
	return nil
}

// Debit is called per accepted item (for generation) or once (for check).
// For PDF: calls=1 on first accepted item of a session, 0 on subsequent; pages=PDF page count on first, 0 after.
// Keep it simple: callers pass whatever increments they want.
func (q *AIQuotaService) Debit(ctx context.Context, userID string, feat ai.FeatureKey, calls, pages int) error {
	date := dayKey(q.now())
	// Ensure row exists.
	if _, _, _, _, err := q.loadOrCreate(ctx, userID, date); err != nil {
		return err
	}
	var col1, col2 string
	switch feat {
	case ai.FeaturePrompt:
		col1 = "prompt_calls"
	case ai.FeaturePDF:
		col1 = "pdf_calls"
		col2 = "pdf_pages"
	case ai.FeatureCheck:
		col1 = "check_calls"
	default:
		return fmt.Errorf("unknown feature: %s", feat)
	}
	stmt := fmt.Sprintf(`UPDATE ai_quota_daily SET %s = %s + ? WHERE user_id=? AND date=?`, col1, col1)
	if _, err := q.db.ExecContext(ctx, stmt, calls, userID, date); err != nil {
		return err
	}
	if col2 != "" && pages > 0 {
		stmt2 := fmt.Sprintf(`UPDATE ai_quota_daily SET %s = %s + ? WHERE user_id=? AND date=?`, col2, col2)
		if _, err := q.db.ExecContext(ctx, stmt2, pages, userID, date); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot returns today's counters for the quota endpoint.
type QuotaSnapshot struct {
	SubscriptionActive bool
	PromptUsed         int
	PromptLimit        int
	PDFUsed            int
	PDFLimit           int
	PDFPagesUsed       int
	PDFPagesLimit      int
	CheckUsed          int
	CheckLimit         int
	ResetAt            time.Time
}

func (q *AIQuotaService) Snapshot(ctx context.Context, userID string, entitled bool) (QuotaSnapshot, error) {
	now := q.now()
	date := dayKey(now)
	prompt, pdf, pages, check, err := q.loadOrCreate(ctx, userID, date)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	return QuotaSnapshot{
		SubscriptionActive: entitled,
		PromptUsed:         prompt,
		PromptLimit:        q.limits.PromptCalls,
		PDFUsed:            pdf,
		PDFLimit:           q.limits.PDFCalls,
		PDFPagesUsed:       pages,
		PDFPagesLimit:      q.limits.PDFPages,
		CheckUsed:          check,
		CheckLimit:         q.limits.CheckCalls,
		ResetAt:            nextMidnight(now),
	}, nil
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `go test ./api/service/ -run TestQuotaService_ -v`
Expected: 4 tests PASS.

- [ ] **Step 4: Commit**

```bash
git add api/service/aiQuotaService.go pkg/ai/feature.go
git commit -m "feat(ai): quota service with per-feature daily limits"
```

---

## Phase 3 — Provider layer

### Task 3.1: Provider interface + message types

**Files:**
- Create: `pkg/ai/messages.go`
- Create: `internal/aiProvider/provider.go`

- [ ] **Step 1: Define message / chunk types in `pkg/ai/messages.go`**

```go
package ai

import "encoding/json"

// AIRole is the role of a message part.
type AIRole string

const (
	RoleSystem AIRole = "system"
	RoleUser   AIRole = "user"
)

// AIContent is either a text block or an image block.
type AIContent struct {
	Type     string `json:"type"`              // "text" | "image"
	Text     string `json:"text,omitempty"`
	ImageB64 string `json:"image_b64,omitempty"`
	MimeType string `json:"mime,omitempty"`
}

type AIMessage struct {
	Role    AIRole      `json:"role"`
	Content []AIContent `json:"content"`
}

// ChunkKind describes what a pipeline chunk carries.
type ChunkKind string

const (
	ChunkItem     ChunkKind = "item"
	ChunkProgress ChunkKind = "progress"
	ChunkDone     ChunkKind = "done"
	ChunkError    ChunkKind = "error"
)

type ProgressInfo struct {
	Phase string `json:"phase"`
	Page  int    `json:"page,omitempty"`
	Total int    `json:"total,omitempty"`
}

type AIError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type AIChunk struct {
	Kind     ChunkKind       `json:"-"`
	Item     json.RawMessage `json:"-"`
	Progress *ProgressInfo   `json:"-"`
	Err      *AIError        `json:"-"`
}
```

- [ ] **Step 2: Define the provider interface**

Create `internal/aiProvider/provider.go`:

```go
package aiProvider

import (
	"context"

	"github.com/martonroux/go-study-buddy/pkg/ai"
)

// Event is what the provider emits. Internal to the provider layer;
// the pipeline translates these into ai.AIChunk.
type EventKind string

const (
	EventDelta     EventKind = "delta"      // raw text token(s)
	EventUsage     EventKind = "usage"      // token counts
	EventRequestID EventKind = "request_id" // provider-specific request id
	EventDone      EventKind = "done"
	EventError     EventKind = "error"
)

type Event struct {
	Kind         EventKind
	Delta        string
	InputTokens  int
	OutputTokens int
	RequestID    string
	Err          error
}

type Provider interface {
	Stream(ctx context.Context, req StreamRequest) (<-chan Event, error)
}

type StreamRequest struct {
	Messages  []ai.AIMessage
	Schema    map[string]any // JSON Schema to steer the model toward the desired shape
	MaxTokens int
	Model     string
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add pkg/ai/messages.go internal/aiProvider/provider.go
git commit -m "feat(ai): provider interface + message types"
```

### Task 3.2: Fake provider for tests

**Files:**
- Create: `internal/aiProvider/fake.go`

- [ ] **Step 1: Implement `FakeProvider`**

```go
package aiProvider

import (
	"context"
	"errors"
	"time"
)

// FakeProvider is a scriptable provider for tests. Set Events to the
// sequence of events you want emitted; set Err to fail Stream() up front.
// If DelayBetween is nonzero, each event waits that long before being sent.
type FakeProvider struct {
	Events       []Event
	DelayBetween time.Duration
	Err          error
}

func (f *FakeProvider) Stream(ctx context.Context, req StreamRequest) (<-chan Event, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	ch := make(chan Event, len(f.Events))
	go func() {
		defer close(ch)
		for _, e := range f.Events {
			if f.DelayBetween > 0 {
				select {
				case <-ctx.Done():
					ch <- Event{Kind: EventError, Err: errors.New("cancelled")}
					return
				case <-time.After(f.DelayBetween):
				}
			}
			select {
			case <-ctx.Done():
				ch <- Event{Kind: EventError, Err: errors.New("cancelled")}
				return
			case ch <- e:
			}
		}
	}()
	return ch, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: ok.

- [ ] **Step 3: Commit**

```bash
git add internal/aiProvider/fake.go
git commit -m "test(ai): fake provider"
```

### Task 3.3: PDF → image converter

**Files:**
- Modify: `go.mod` / `go.sum` (via `go get`)
- Create: `internal/aiProvider/pdf.go`
- Create: `internal/aiProvider/pdf_test.go`
- Add: `testutil/fixtures/sample.pdf` (a small 2-page PDF — commit a real one)

- [ ] **Step 1: Add the PDF library**

Run: `go get github.com/gen2brain/go-fitz@latest`
(go-fitz wraps MuPDF. If licensing/deps are a concern, swap to `github.com/ledongthuc/pdf` for text-only — but the spec decided vision-only, so go-fitz is correct.)

- [ ] **Step 2: Commit a small sample PDF under `testutil/fixtures/sample.pdf`**

Produce a 2-page test PDF locally (`echo 'page 1' | ps2pdf - page1.pdf`, similar for page2, then `pdfunite page1.pdf page2.pdf testutil/fixtures/sample.pdf`). Keep it <100 KB.

- [ ] **Step 3: Write the failing test**

Create `internal/aiProvider/pdf_test.go`:

```go
package aiProvider

import (
	"os"
	"testing"
)

func TestConvertPDF_ReturnsOneImagePerPage(t *testing.T) {
	bytes, err := os.ReadFile("../../testutil/fixtures/sample.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	imgs, err := ConvertPDFToPNGs(bytes, 150 /*dpi*/)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(imgs))
	}
	for i, img := range imgs {
		if len(img) == 0 {
			t.Errorf("page %d empty", i)
		}
	}
}

func TestConvertPDF_RejectsNonPDF(t *testing.T) {
	_, err := ConvertPDFToPNGs([]byte("not a pdf"), 150)
	if err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/aiProvider/ -run TestConvertPDF_ -v`
Expected: FAIL — `ConvertPDFToPNGs` undefined.

- [ ] **Step 5: Implement**

Create `internal/aiProvider/pdf.go`:

```go
package aiProvider

import (
	"bytes"
	"fmt"
	"image/png"

	"github.com/gen2brain/go-fitz"
)

// ConvertPDFToPNGs renders each page at the given DPI and returns PNG-encoded bytes.
// Returns an error for malformed PDFs or empty files.
func ConvertPDFToPNGs(pdf []byte, dpi int) ([][]byte, error) {
	if len(pdf) == 0 {
		return nil, fmt.Errorf("empty pdf")
	}
	doc, err := fitz.NewFromMemory(pdf)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer doc.Close()

	pages := doc.NumPage()
	out := make([][]byte, 0, pages)
	for i := 0; i < pages; i++ {
		img, err := doc.ImageDPI(i, float64(dpi))
		if err != nil {
			return nil, fmt.Errorf("render page %d: %w", i, err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode page %d: %w", i, err)
		}
		out = append(out, buf.Bytes())
	}
	return out, nil
}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/aiProvider/ -run TestConvertPDF_ -v`
Expected: both tests PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/aiProvider/pdf.go internal/aiProvider/pdf_test.go testutil/fixtures/sample.pdf
git commit -m "feat(ai): PDF page rendering via go-fitz"
```

### Task 3.4: Claude provider (real HTTP)

**Files:**
- Create: `internal/aiProvider/claude.go`

This task does **not** include a test — we don't hit the real API in tests (established in the spec). A small wire-format smoke test can be added later against the fake.

- [ ] **Step 1: Implement the Claude provider**

Create `internal/aiProvider/claude.go`:

```go
package aiProvider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/martonroux/go-study-buddy/pkg/ai"
)

type ClaudeProvider struct {
	APIKey  string
	BaseURL string        // default https://api.anthropic.com
	Client  *http.Client  // default &http.Client{Timeout: 90s}
	Model   string        // default "claude-opus-4-7"
}

func NewClaudeProvider(apiKey string) *ClaudeProvider {
	return &ClaudeProvider{
		APIKey:  apiKey,
		BaseURL: "https://api.anthropic.com",
		Client:  &http.Client{Timeout: 90 * time.Second},
		Model:   "claude-opus-4-7",
	}
}

type claudeReqMessage struct {
	Role    string        `json:"role"`
	Content []claudePart  `json:"content"`
}

type claudePart struct {
	Type   string           `json:"type"`
	Text   string           `json:"text,omitempty"`
	Source *claudeImgSource `json:"source,omitempty"`
}

type claudeImgSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/png"
	Data      string `json:"data"`
}

type claudeRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []claudeReqMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
}

func (p *ClaudeProvider) Stream(ctx context.Context, req StreamRequest) (<-chan Event, error) {
	sys, msgs := toClaude(req.Messages)
	body := claudeRequest{
		Model:     p.modelOr(req.Model),
		System:    sys,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("provider http %d", resp.StatusCode)
	}

	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		reqID := resp.Header.Get("request-id")
		if reqID != "" {
			ch <- Event{Kind: EventRequestID, RequestID: reqID}
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			var evt struct {
				Type    string `json:"type"`
				Delta   struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage,omitempty"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			switch evt.Type {
			case "content_block_delta":
				if evt.Delta.Text != "" {
					ch <- Event{Kind: EventDelta, Delta: evt.Delta.Text}
				}
			case "message_delta", "message_start":
				if evt.Usage != nil {
					ch <- Event{Kind: EventUsage, InputTokens: evt.Usage.InputTokens, OutputTokens: evt.Usage.OutputTokens}
				}
			case "message_stop":
				ch <- Event{Kind: EventDone}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Event{Kind: EventError, Err: err}
			return
		}
		ch <- Event{Kind: EventError, Err: errors.New("stream closed without message_stop")}
	}()
	return ch, nil
}

func (p *ClaudeProvider) modelOr(override string) string {
	if override != "" {
		return override
	}
	return p.Model
}

func toClaude(msgs []ai.AIMessage) (system string, out []claudeReqMessage) {
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			for _, c := range m.Content {
				if c.Type == "text" {
					system += c.Text + "\n"
				}
			}
			continue
		}
		parts := make([]claudePart, 0, len(m.Content))
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				parts = append(parts, claudePart{Type: "text", Text: c.Text})
			case "image":
				parts = append(parts, claudePart{
					Type: "image",
					Source: &claudeImgSource{
						Type:      "base64",
						MediaType: c.MimeType,
						Data:      c.ImageB64,
					},
				})
			}
		}
		out = append(out, claudeReqMessage{Role: string(m.Role), Content: parts})
	}
	return strings.TrimSpace(system), out
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: ok.

- [ ] **Step 3: Commit**

```bash
git add internal/aiProvider/claude.go
git commit -m "feat(ai): claude provider (streaming HTTP client)"
```

---

## Phase 4 — The pipeline primitive

### Task 4.1: Pipeline tests (entitlement + quota + happy path)

**Files:**
- Create: `api/service/aiService_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/martonroux/go-study-buddy/internal/aiProvider"
	"github.com/martonroux/go-study-buddy/pkg/ai"
	"github.com/martonroux/go-study-buddy/testutil"
)

func newService(t *testing.T, prov aiProvider.Provider, entitled bool) (*AIService, string) {
	t.Helper()
	db := testutil.NewTestDB(t)
	uid := testutil.CreateUser(t, db, "u1", "alice", entitled)
	q := NewAIQuotaService(db, DefaultQuotaLimits(), func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	})
	return NewAIService(db, prov, q, func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	}), uid
}

func TestPipeline_NotEntitled(t *testing.T) {
	prov := &aiProvider.FakeProvider{}
	svc, uid := newService(t, prov, false)

	ch, _, err := svc.RunStructuredGeneration(context.Background(), AIRequest{
		UserID: uid, Feature: ai.FeaturePrompt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := drain(ch)
	if len(got) != 1 || got[0].Kind != ai.ChunkError || got[0].Err.Kind != "not_entitled" {
		t.Fatalf("expected not_entitled, got %+v", got)
	}
}

func TestPipeline_QuotaExceeded(t *testing.T) {
	prov := &aiProvider.FakeProvider{}
	svc, uid := newService(t, prov, true)
	svc.quota.(*AIQuotaService).limits.PromptCalls = 0

	ch, _, err := svc.RunStructuredGeneration(context.Background(), AIRequest{
		UserID: uid, Feature: ai.FeaturePrompt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := drain(ch)
	if len(got) != 1 || got[0].Err.Kind != "quota_exceeded" {
		t.Fatalf("got %+v", got)
	}
}

func TestPipeline_HappyPath_TwoItems(t *testing.T) {
	prov := &aiProvider.FakeProvider{
		Events: []aiProvider.Event{
			{Kind: aiProvider.EventDelta, Delta: `[{"title":"A","question":"Q1","answer":"A1"}`},
			{Kind: aiProvider.EventDelta, Delta: `,{"title":"B","question":"Q2","answer":"A2"}]`},
			{Kind: aiProvider.EventUsage, InputTokens: 10, OutputTokens: 20},
			{Kind: aiProvider.EventDone},
		},
	}
	svc, uid := newService(t, prov, true)
	ch, _, err := svc.RunStructuredGeneration(context.Background(), AIRequest{
		UserID:  uid,
		Feature: ai.FeaturePrompt,
		Schema:  map[string]any{"type": "object", "required": []string{"title", "question", "answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(ch)
	items := 0
	for _, c := range got {
		if c.Kind == ai.ChunkItem {
			items++
			var card struct{ Title, Question, Answer string }
			_ = json.Unmarshal(c.Item, &card)
			if card.Title == "" {
				t.Error("empty title")
			}
		}
	}
	if items != 2 {
		t.Fatalf("expected 2 items, got %d", items)
	}
}

func drain(ch <-chan ai.AIChunk) []ai.AIChunk {
	var out []ai.AIChunk
	for c := range ch {
		out = append(out, c)
	}
	return out
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./api/service/ -run TestPipeline_ -v`
Expected: FAIL — `NewAIService`, `AIRequest`, `RunStructuredGeneration` undefined.

- [ ] **Step 3: Commit the failing tests**

```bash
git add api/service/aiService_test.go
git commit -m "test(ai): pipeline happy-path + entitlement + quota tests"
```

### Task 4.2: Pipeline implementation — skeleton + entitlement + quota

**Files:**
- Create: `api/service/aiService.go`
- Create: `api/service/aiJobRepo.go`

- [ ] **Step 1: Implement the job repository**

Create `api/service/aiJobRepo.go`:

```go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AIJobRow struct {
	ID            int64
	UserID        string
	Feature       string
	Status        string // running | complete | failed | cancelled
	SubjectID     *int64
	FlashcardID   *int64
	RequestParams json.RawMessage
	InputHash     string
	PDFPages      int
	Provider      string
	StartedAt     time.Time
}

type AIJobFinalize struct {
	Status        string
	ItemsEmitted  int
	ItemsDropped  int
	InputTokens   int
	OutputTokens  int
	CostCents     int
	ProviderReqID string
	ErrorKind     string
	ErrorMessage  string
	FinishedAt    time.Time
}

type AIJobRepo struct{ db *sql.DB }

func NewAIJobRepo(db *sql.DB) *AIJobRepo { return &AIJobRepo{db: db} }

func (r *AIJobRepo) Create(ctx context.Context, j AIJobRow) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_jobs
		  (user_id, feature, status, subject_id, flashcard_id,
		   request_params, input_hash, pdf_page_count, provider, started_at)
		 VALUES (?, ?, 'running', ?, ?, ?, ?, ?, ?, ?)`,
		j.UserID, j.Feature, j.SubjectID, j.FlashcardID,
		string(j.RequestParams), j.InputHash, j.PDFPages, j.Provider,
		j.StartedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *AIJobRepo) Finalize(ctx context.Context, jobID int64, f AIJobFinalize) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE ai_jobs
		  SET status=?, items_emitted=?, items_dropped=?,
		      input_tokens=?, output_tokens=?, cost_cents=?,
		      provider_req_id=?, error_kind=?, error_message=?, finished_at=?
		  WHERE id=?`,
		f.Status, f.ItemsEmitted, f.ItemsDropped,
		f.InputTokens, f.OutputTokens, f.CostCents,
		f.ProviderReqID, nullIfEmpty(f.ErrorKind), nullIfEmpty(f.ErrorMessage),
		f.FinishedAt.UTC().Format(time.RFC3339),
		jobID)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CountRunning returns the number of status='running' jobs for a user (for concurrent-gen cap).
func (r *AIJobRepo) CountRunning(ctx context.Context, userID, feature string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ai_jobs WHERE user_id=? AND feature=? AND status='running'`,
		userID, feature).Scan(&n)
	return n, err
}
```

- [ ] **Step 2: Implement the pipeline skeleton**

Create `api/service/aiService.go`:

```go
package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/martonroux/go-study-buddy/internal/aiProvider"
	"github.com/martonroux/go-study-buddy/pkg/ai"
)

type AIRequest struct {
	UserID       string
	Feature      ai.FeatureKey
	SubjectID    *int64
	FlashcardID  *int64
	Messages     []ai.AIMessage
	Schema       map[string]any
	MaxTokens    int
	PDFPages     int
	InputHash    string // optional; hash of prompt or PDF bytes
}

type quotaIface interface {
	Check(ctx context.Context, userID string, feat ai.FeatureKey, pages int) error
	Debit(ctx context.Context, userID string, feat ai.FeatureKey, calls, pages int) error
}

type AIService struct {
	db       *sql.DB
	provider aiProvider.Provider
	quota    quotaIface
	jobs     *AIJobRepo
	now      func() time.Time
}

func NewAIService(db *sql.DB, prov aiProvider.Provider, q quotaIface, now func() time.Time) *AIService {
	if now == nil {
		now = time.Now
	}
	return &AIService{db: db, provider: prov, quota: q, jobs: NewAIJobRepo(db), now: now}
}

// RunStructuredGeneration is the only AI entry point. Returns a channel of
// validated chunks plus the job ID for audit. On entitlement / quota errors
// the channel emits a single ChunkError then closes, and jobID is 0.
func (s *AIService) RunStructuredGeneration(ctx context.Context, req AIRequest) (<-chan ai.AIChunk, int64, error) {
	ch := make(chan ai.AIChunk, 8)

	// 1. Entitlement
	entitled, err := s.isEntitled(ctx, req.UserID)
	if err != nil {
		return nil, 0, err
	}
	if !entitled {
		go func() {
			ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "not_entitled", Message: "AI features require an active subscription."}}
			close(ch)
		}()
		return ch, 0, nil
	}

	// 2. Quota
	if err := s.quota.Check(ctx, req.UserID, req.Feature, req.PDFPages); err != nil {
		go func() {
			var qErr *QuotaError
			if asQuota(err, &qErr) {
				ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "quota_exceeded", Message: qErr.Detail}}
			} else {
				ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "internal", Message: err.Error()}}
			}
			close(ch)
		}()
		return ch, 0, nil
	}

	// 3. Concurrent-generation cap (generation features only)
	if req.Feature == ai.FeaturePrompt || req.Feature == ai.FeaturePDF {
		n, err := s.jobs.CountRunning(ctx, req.UserID, string(req.Feature))
		if err != nil {
			close(ch)
			return ch, 0, err
		}
		if n >= 1 {
			go func() {
				ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "already_running", Message: "A generation is already in progress."}}
				close(ch)
			}()
			return ch, 0, nil
		}
	}

	// 4. Job row
	params, _ := json.Marshal(map[string]any{
		"maxTokens": req.MaxTokens,
	})
	hash := req.InputHash
	if hash == "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", req.Messages)))
		hash = hex.EncodeToString(sum[:])
	}
	jobID, err := s.jobs.Create(ctx, AIJobRow{
		UserID:        req.UserID,
		Feature:       string(req.Feature),
		SubjectID:     req.SubjectID,
		FlashcardID:   req.FlashcardID,
		RequestParams: params,
		InputHash:     hash,
		PDFPages:      req.PDFPages,
		Provider:      "anthropic",
		StartedAt:     s.now(),
	})
	if err != nil {
		close(ch)
		return ch, 0, err
	}

	// 5. Hand off to the streaming consumer.
	go s.runAndEmit(ctx, req, jobID, ch)
	return ch, jobID, nil
}

func (s *AIService) isEntitled(ctx context.Context, userID string) (bool, error) {
	var active int
	err := s.db.QueryRowContext(ctx,
		`SELECT ai_subscription_active FROM users WHERE id=?`, userID).Scan(&active)
	if err != nil {
		return false, err
	}
	return active == 1, nil
}

// runAndEmit is the inner loop: provider stream → JSON parser → schema validation → channel.
// Placeholder in Task 4.2; filled in in Task 4.3.
func (s *AIService) runAndEmit(ctx context.Context, req AIRequest, jobID int64, ch chan<- ai.AIChunk) {
	defer close(ch)
	// PLACEHOLDER — implemented in Task 4.3.
	ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "internal", Message: "runAndEmit not implemented"}}
	_ = s.jobs.Finalize(ctx, jobID, AIJobFinalize{Status: "failed", ErrorKind: "internal", FinishedAt: s.now()})
}

func asQuota(err error, out **QuotaError) bool {
	q, ok := err.(*QuotaError)
	if ok {
		*out = q
	}
	return ok
}
```

- [ ] **Step 3: Run the entitlement + quota tests**

Run: `go test ./api/service/ -run "TestPipeline_NotEntitled|TestPipeline_QuotaExceeded" -v`
Expected: both PASS. The happy-path test still fails because `runAndEmit` is a placeholder.

- [ ] **Step 4: Commit**

```bash
git add api/service/aiService.go api/service/aiJobRepo.go
git commit -m "feat(ai): pipeline skeleton + entitlement + quota enforcement"
```

### Task 4.3: Streaming JSON parser + schema validation + happy path

**Files:**
- Create: `pkg/ai/streamparse.go`
- Create: `pkg/ai/streamparse_test.go`
- Modify: `api/service/aiService.go`

- [ ] **Step 1: Test the streaming array parser**

Create `pkg/ai/streamparse_test.go`:

```go
package ai

import "testing"

func TestArrayParser_EmitsEachCompleteObject(t *testing.T) {
	p := NewArrayStreamParser()
	var got []string
	p.OnItem = func(raw []byte) { got = append(got, string(raw)) }

	for _, chunk := range []string{
		`[{"a":1}`,
		`,{"a":2}`,
		`,{"a":3}]`,
	} {
		if err := p.Feed([]byte(chunk)); err != nil {
			t.Fatalf("feed: %v", err)
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %v", got)
	}
}

func TestArrayParser_HandlesLeadingWhitespace(t *testing.T) {
	p := NewArrayStreamParser()
	var got []string
	p.OnItem = func(raw []byte) { got = append(got, string(raw)) }

	chunks := []string{"   \n[", ` {"x": 1} `, `]`}
	for _, c := range chunks {
		_ = p.Feed([]byte(c))
	}
	if len(got) != 1 || got[0] != `{"x": 1}` {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./pkg/ai/ -run TestArrayParser_ -v`
Expected: FAIL — `NewArrayStreamParser` undefined.

- [ ] **Step 3: Implement the parser**

Create `pkg/ai/streamparse.go`:

```go
package ai

import (
	"errors"
	"strings"
)

// ArrayStreamParser consumes raw text deltas from a streaming model
// and emits each top-level JSON object inside a top-level array.
// It tolerates leading whitespace, commas between items, and trailing ].
//
// Usage:
//   p := NewArrayStreamParser()
//   p.OnItem = func(raw []byte) { ... }
//   for each delta: p.Feed([]byte(delta))
type ArrayStreamParser struct {
	OnItem func(raw []byte)
	buf    strings.Builder
	state  parseState
	depth  int
	inStr  bool
	esc    bool
	start  int
}

type parseState int

const (
	stateStart parseState = iota
	stateArray
	stateObject
	stateDone
)

func NewArrayStreamParser() *ArrayStreamParser {
	return &ArrayStreamParser{state: stateStart}
}

func (p *ArrayStreamParser) Feed(b []byte) error {
	if p.state == stateDone {
		return nil
	}
	p.buf.Write(b)
	s := p.buf.String()
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch p.state {
		case stateStart:
			if c == '[' {
				p.state = stateArray
			} else if !isWS(c) {
				return errors.New("expected '[' at start of stream")
			}
		case stateArray:
			if c == '{' {
				p.state = stateObject
				p.depth = 1
				p.start = i
			} else if c == ']' {
				p.state = stateDone
				p.buf.Reset()
				return nil
			}
		case stateObject:
			if p.inStr {
				if p.esc {
					p.esc = false
				} else if c == '\\' {
					p.esc = true
				} else if c == '"' {
					p.inStr = false
				}
				continue
			}
			switch c {
			case '"':
				p.inStr = true
			case '{':
				p.depth++
			case '}':
				p.depth--
				if p.depth == 0 {
					raw := s[p.start : i+1]
					if p.OnItem != nil {
						p.OnItem([]byte(raw))
					}
					p.state = stateArray
				}
			}
		}
	}
	// Keep the unconsumed tail for next Feed.
	if p.state == stateObject {
		// partial object — keep from p.start on
		remaining := s[p.start:]
		p.buf.Reset()
		p.buf.WriteString(remaining)
		p.start = 0
	} else {
		// Everything processed; buffer can be reset except we may be mid-whitespace.
		p.buf.Reset()
	}
	return nil
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
```

- [ ] **Step 4: Run parser tests**

Run: `go test ./pkg/ai/ -run TestArrayParser_ -v`
Expected: both PASS.

- [ ] **Step 5: Wire up `runAndEmit` in `aiService.go`**

Replace the placeholder `runAndEmit` in `api/service/aiService.go`:

```go
func (s *AIService) runAndEmit(ctx context.Context, req AIRequest, jobID int64, ch chan<- ai.AIChunk) {
	defer close(ch)

	fin := AIJobFinalize{Status: "running"}
	defer func() {
		fin.FinishedAt = s.now()
		_ = s.jobs.Finalize(context.Background(), jobID, fin)
	}()

	stream, err := s.streamWithRetry(ctx, req)
	if err != nil {
		kind := classifyProviderErr(err)
		ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: kind, Message: err.Error()}}
		fin.Status = "failed"
		fin.ErrorKind = kind
		fin.ErrorMessage = err.Error()
		return
	}

	parser := ai.NewArrayStreamParser()
	emitted, dropped := 0, 0
	parser.OnItem = func(raw []byte) {
		if req.Schema != nil {
			if !validateAgainstSchema(raw, req.Schema) {
				dropped++
				return
			}
		}
		// Debit quota per accepted item.
		pages := 0
		calls := 1
		if req.Feature == ai.FeaturePDF && emitted == 0 {
			pages = req.PDFPages
		} else if req.Feature == ai.FeaturePDF {
			calls = 0 // one call per PDF, not per card
		}
		_ = s.quota.Debit(context.Background(), req.UserID, req.Feature, calls, pages)
		emitted++
		ch <- ai.AIChunk{Kind: ai.ChunkItem, Item: raw}
	}

	for evt := range stream {
		select {
		case <-ctx.Done():
			fin.Status = "cancelled"
			fin.ErrorKind = "cancelled"
			return
		default:
		}
		switch evt.Kind {
		case aiProvider.EventDelta:
			_ = parser.Feed([]byte(evt.Delta))
		case aiProvider.EventUsage:
			fin.InputTokens = evt.InputTokens
			fin.OutputTokens = evt.OutputTokens
		case aiProvider.EventRequestID:
			fin.ProviderReqID = evt.RequestID
		case aiProvider.EventDone:
			fin.Status = "complete"
			fin.ItemsEmitted = emitted
			fin.ItemsDropped = dropped
			ch <- ai.AIChunk{Kind: ai.ChunkDone}
			return
		case aiProvider.EventError:
			kind := classifyProviderErr(evt.Err)
			ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: kind, Message: evt.Err.Error()}}
			fin.Status = "failed"
			fin.ErrorKind = kind
			fin.ErrorMessage = evt.Err.Error()
			fin.ItemsEmitted = emitted
			fin.ItemsDropped = dropped
			return
		}
	}
	fin.Status = "failed"
	fin.ErrorKind = "provider_timeout"
	fin.ItemsEmitted = emitted
	fin.ItemsDropped = dropped
	ch <- ai.AIChunk{Kind: ai.ChunkError, Err: &ai.AIError{Kind: "provider_timeout", Message: "stream ended without done"}}
}

func validateAgainstSchema(raw []byte, schema map[string]any) bool {
	// Minimal v1 check: object with all keys in schema["required"] present.
	req, _ := schema["required"].([]string)
	if len(req) == 0 {
		// Allow []any shape too
		if anyList, ok := schema["required"].([]any); ok {
			for _, v := range anyList {
				if s, ok := v.(string); ok {
					req = append(req, s)
				}
			}
		}
	}
	if len(req) == 0 {
		return true
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	for _, k := range req {
		if _, ok := obj[k]; !ok {
			return false
		}
	}
	return true
}

// streamWithRetry calls the provider; on provider_5xx / timeout / rate-limit it retries ONCE.
func (s *AIService) streamWithRetry(ctx context.Context, req AIRequest) (<-chan aiProvider.Event, error) {
	call := func() (<-chan aiProvider.Event, error) {
		return s.provider.Stream(ctx, aiProvider.StreamRequest{
			Messages:  req.Messages,
			Schema:    req.Schema,
			MaxTokens: req.MaxTokens,
		})
	}
	stream, err := call()
	if err == nil {
		return stream, nil
	}
	if !isTransient(err) {
		return nil, err
	}
	time.Sleep(400 * time.Millisecond)
	return call()
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "timeout") || contains(msg, "http 5") || contains(msg, "429")
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func classifyProviderErr(err error) string {
	if err == nil {
		return "internal"
	}
	msg := err.Error()
	switch {
	case contains(msg, "cancelled"):
		return "cancelled"
	case contains(msg, "timeout"):
		return "provider_timeout"
	case contains(msg, "429"):
		return "provider_rate_limit"
	case contains(msg, "http 5"):
		return "provider_5xx"
	case contains(msg, "content policy"), contains(msg, "content_policy"):
		return "content_policy"
	}
	return "internal"
}
```

Also add the imports: `"strings"`.

- [ ] **Step 6: Run the full pipeline test suite**

Run: `go test ./api/service/ -run TestPipeline_ -v`
Expected: 3 tests PASS (entitlement, quota, happy path with 2 items).

- [ ] **Step 7: Commit**

```bash
git add pkg/ai/streamparse.go pkg/ai/streamparse_test.go api/service/aiService.go
git commit -m "feat(ai): streaming JSON parser + pipeline happy path + retry"
```

### Task 4.4: Pipeline failure + cancellation tests

**Files:**
- Modify: `api/service/aiService_test.go`

- [ ] **Step 1: Add failure tests**

Append to `api/service/aiService_test.go`:

```go
func TestPipeline_ProviderErrorSurfaces(t *testing.T) {
	prov := &aiProvider.FakeProvider{
		Events: []aiProvider.Event{
			{Kind: aiProvider.EventDelta, Delta: `[{"title":"A","question":"Q","answer":"A"}`},
			{Kind: aiProvider.EventError, Err: errors.New("http 503 Service Unavailable")},
		},
	}
	svc, uid := newService(t, prov, true)

	ch, _, err := svc.RunStructuredGeneration(context.Background(), AIRequest{
		UserID: uid, Feature: ai.FeaturePrompt,
		Schema: map[string]any{"required": []string{"title", "question", "answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(ch)
	var errKind string
	items := 0
	for _, c := range got {
		if c.Kind == ai.ChunkItem {
			items++
		}
		if c.Kind == ai.ChunkError {
			errKind = c.Err.Kind
		}
	}
	if items != 1 {
		t.Errorf("expected 1 partial item, got %d", items)
	}
	if errKind != "provider_5xx" {
		t.Errorf("expected provider_5xx, got %q", errKind)
	}
}

func TestPipeline_SchemaViolationDropped(t *testing.T) {
	prov := &aiProvider.FakeProvider{
		Events: []aiProvider.Event{
			{Kind: aiProvider.EventDelta, Delta: `[{"title":"A","question":"Q","answer":"A"}`},
			{Kind: aiProvider.EventDelta, Delta: `,{"title":"only title"}`},
			{Kind: aiProvider.EventDelta, Delta: `,{"title":"C","question":"Q","answer":"A"}]`},
			{Kind: aiProvider.EventDone},
		},
	}
	svc, uid := newService(t, prov, true)

	ch, _, err := svc.RunStructuredGeneration(context.Background(), AIRequest{
		UserID: uid, Feature: ai.FeaturePrompt,
		Schema: map[string]any{"required": []string{"title", "question", "answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(ch)
	items := 0
	for _, c := range got {
		if c.Kind == ai.ChunkItem {
			items++
		}
	}
	if items != 2 {
		t.Errorf("expected 2 valid items (1 dropped), got %d", items)
	}
}

func TestPipeline_Cancellation(t *testing.T) {
	prov := &aiProvider.FakeProvider{
		Events: []aiProvider.Event{
			{Kind: aiProvider.EventDelta, Delta: `[{"title":"A","question":"Q","answer":"A"}`},
		},
		DelayBetween: 50 * time.Millisecond,
	}
	svc, uid := newService(t, prov, true)

	ctx, cancel := context.WithCancel(context.Background())
	ch, _, err := svc.RunStructuredGeneration(ctx, AIRequest{
		UserID: uid, Feature: ai.FeaturePrompt,
		Schema: map[string]any{"required": []string{"title", "question", "answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_ = drain(ch) // just ensure we terminate — content tested by the error classifier elsewhere
}
```

Add import: `"errors"`.

- [ ] **Step 2: Run to verify the new tests pass**

Run: `go test ./api/service/ -run TestPipeline_ -v`
Expected: 6 tests PASS.

- [ ] **Step 3: Commit**

```bash
git add api/service/aiService_test.go
git commit -m "test(ai): pipeline failure + cancellation cases"
```

---

## Phase 5 — Prompts & schemas

### Task 5.1: Embed prompt templates

**Files:**
- Create: `pkg/ai/prompts/generate_prompt.txt`
- Create: `pkg/ai/prompts/generate_pdf.txt`
- Create: `pkg/ai/prompts/check_flashcard.txt`
- Create: `pkg/ai/prompts.go`

- [ ] **Step 1: Write `generate_prompt.txt`**

```
You are a study-companion LLM. The user will provide a topic or notes via a prompt.
Produce a JSON array of flashcards that cover the material.

Rules:
- Output MUST be a JSON array of objects with keys "title", "question", "answer", and "difficulty" (integer 0=easy, 1=medium, 2=hard).
- No preamble, no explanation, no markdown fences. The first character must be `[`, the last must be `]`.
- Keep questions concrete and answers self-contained.
- Follow the user-provided style ({{STYLE}}).
- Target count: {{TARGET_COUNT}} (0 means pick a sensible number).
- Additional focus: {{FOCUS}}

User prompt:
{{PROMPT}}
```

- [ ] **Step 2: Write `generate_pdf.txt`**

```
You are a study-companion LLM. The user has uploaded a PDF. Page images are attached.
Produce study flashcards that cover the material. If auto-chapters is enabled, you
may emit the output as a JSON array of cards where each card has a "chapter" field
(short title, <=40 chars). If auto-chapters is disabled, omit "chapter".

Rules:
- Output MUST be a JSON array. First char `[`, last char `]`, no preamble.
- Each object: "title", "question", "answer", "difficulty" (0|1|2), and optionally "chapter".
- Use the style ({{STYLE}}).
- Coverage: {{COVERAGE}}. Interpret as follows:
  - "essentials": keep only the core ideas the student must know.
  - "balanced": core ideas plus the most useful examples.
  - "comprehensive": cover everything of study value in the source.
  Pick the card count yourself based on the PDF length; do not pad.
- Additional focus: {{FOCUS}}
- Auto-chapters: {{AUTO_CHAPTERS}}

Use the attached page images as the source of truth. Prefer content from the PDF
over anything you might "know."
```

- [ ] **Step 3: Write `check_flashcard.txt`**

```
You are reviewing a single study flashcard for factual accuracy, style, and typos.

Return a SINGLE JSON object (not an array) with this exact shape:

{
  "verdict": "ok" | "minor_issues" | "major_issues",
  "findings": [
    {"kind": "factual" | "style" | "typo", "text": "..."}
  ],
  "suggestion": {
    "title":    "...",
    "question": "...",
    "answer":   "..."
  }
}

Rules:
- "suggestion" is ALWAYS present; on "ok" verdicts, echo the input unchanged.
- Be concise in "findings" — one sentence each.
- Do not emit markdown fences or any preamble. First char `{`, last char `}`.

Flashcard under review:
Title:    {{TITLE}}
Question: {{QUESTION}}
Answer:   {{ANSWER}}
```

- [ ] **Step 4: Load templates from Go**

Create `pkg/ai/prompts.go`:

```go
package ai

import (
	_ "embed"
	"strings"
)

//go:embed prompts/generate_prompt.txt
var promptGenerateFromPrompt string

//go:embed prompts/generate_pdf.txt
var promptGenerateFromPDF string

//go:embed prompts/check_flashcard.txt
var promptCheckFlashcard string

func PromptForFeature(f FeatureKey) string {
	switch f {
	case FeaturePrompt:
		return promptGenerateFromPrompt
	case FeaturePDF:
		return promptGenerateFromPDF
	case FeatureCheck:
		return promptCheckFlashcard
	}
	return ""
}

// Render substitutes {{KEY}} placeholders with the provided values.
func Render(template string, vars map[string]string) string {
	for k, v := range vars {
		template = strings.ReplaceAll(template, "{{"+k+"}}", v)
	}
	return template
}
```

- [ ] **Step 5: Verify compilation**

Run: `go build ./...`
Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add pkg/ai/prompts.go pkg/ai/prompts/
git commit -m "feat(ai): embedded prompt templates"
```

---

## Phase 6 — Simple endpoints (quota + admin)

### Task 6.1: `GET /ai/quota`

**Files:**
- Create: `api/handler/aiHandler.go`
- Modify: `cmd/app/main.go` (route registration)

- [ ] **Step 1: Implement the handler**

Create `api/handler/aiHandler.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/martonroux/go-study-buddy/api/service"
	"github.com/martonroux/go-study-buddy/internal/jwt"
)

type AIHandler struct {
	AI    *service.AIService
	Quota *service.AIQuotaService
}

type quotaResp struct {
	SubscriptionActive bool           `json:"subscriptionActive"`
	Prompt             counterSnapshot `json:"prompt"`
	PDF                pdfSnapshot    `json:"pdf"`
	Check              counterSnapshot `json:"check"`
}

type counterSnapshot struct {
	Used    int       `json:"used"`
	Limit   int       `json:"limit"`
	ResetAt time.Time `json:"resetAt"`
}

type pdfSnapshot struct {
	Used       int       `json:"used"`
	Limit      int       `json:"limit"`
	PagesUsed  int       `json:"pagesUsed"`
	PagesLimit int       `json:"pagesLimit"`
	ResetAt    time.Time `json:"resetAt"`
}

func (h *AIHandler) GetQuota(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	entitled, err := h.lookupEntitlement(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}
	snap, err := h.Quota.Snapshot(r.Context(), userID, entitled)
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}
	resp := quotaResp{
		SubscriptionActive: snap.SubscriptionActive,
		Prompt:             counterSnapshot{Used: snap.PromptUsed, Limit: snap.PromptLimit, ResetAt: snap.ResetAt},
		PDF:                pdfSnapshot{Used: snap.PDFUsed, Limit: snap.PDFLimit, PagesUsed: snap.PDFPagesUsed, PagesLimit: snap.PDFPagesLimit, ResetAt: snap.ResetAt},
		Check:              counterSnapshot{Used: snap.CheckUsed, Limit: snap.CheckLimit, ResetAt: snap.ResetAt},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *AIHandler) lookupEntitlement(ctx context.Context, userID string) (bool, error) {
	// The AIService exposes isEntitled privately; for the handler we repeat the query
	// to avoid leaking the internal method. Cheap.
	var active int
	err := h.AI.DB().QueryRowContext(ctx,
		`SELECT ai_subscription_active FROM users WHERE id=?`, userID).Scan(&active)
	if err != nil {
		return false, err
	}
	return active == 1, nil
}
```

Add a public `DB()` accessor to `AIService` (in `api/service/aiService.go`):

```go
func (s *AIService) DB() *sql.DB { return s.db }
```

- [ ] **Step 2: Register the route**

Open `cmd/app/main.go`. Alongside the other protected routes:

```go
aiHandler := &handler.AIHandler{
    AI:    aiService,
    Quota: aiQuotaService,
}
mux.Handle("GET /ai/quota", jwt.RequireVerified(http.HandlerFunc(aiHandler.GetQuota)))
```

- [ ] **Step 3: Manual smoke test**

Run: `go run ./cmd/app` in one terminal. In another, with a valid token:

```
curl -H "Authorization: Bearer <token>" http://localhost:8080/ai/quota
```

Expected: JSON body with `subscriptionActive: false` for a fresh user.

- [ ] **Step 4: Commit**

```bash
git add api/handler/aiHandler.go api/service/aiService.go cmd/app/main.go
git commit -m "feat(ai): GET /ai/quota"
```

### Task 6.2: Dev-only admin endpoint

**Files:**
- Create: `api/handler/adminHandler.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Implement the handler**

Create `api/handler/adminHandler.go`:

```go
package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
)

type AdminHandler struct{ DB *sql.DB }

type setSubReq struct {
	UserID string `json:"user_id"`
	Active bool   `json:"active"`
}

func (h *AdminHandler) SetAISubscription(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ADMIN_API_ENABLED") != "true" {
		http.NotFound(w, r)
		return
	}
	var req setSubReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
		return
	}
	active := 0
	if req.Active {
		active = 1
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE users SET ai_subscription_active=? WHERE id=?`, active, req.UserID)
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Register the route**

```go
adminHandler := &handler.AdminHandler{DB: db}
mux.Handle("POST /admin/set-ai-subscription", http.HandlerFunc(adminHandler.SetAISubscription))
```

Note: no auth middleware — the env var is the only guard, and this is dev-only.

- [ ] **Step 3: Manual smoke test**

```
ADMIN_API_ENABLED=true go run ./cmd/app
# separate terminal:
curl -X POST http://localhost:8080/admin/set-ai-subscription \
    -H 'content-type: application/json' \
    -d '{"user_id":"<your_user_id>","active":true}' -i
```

Expected: `204 No Content`.

- [ ] **Step 4: Commit**

```bash
git add api/handler/adminHandler.go cmd/app/main.go
git commit -m "feat(ai): dev-only admin endpoint to flip ai_subscription_active"
```

---

## Phase 7 — Generation endpoints

### Task 7.1: `POST /ai/generate-flashcards` (SSE)

**Files:**
- Modify: `api/handler/aiHandler.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add the handler**

Append to `api/handler/aiHandler.go`:

```go
import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"

	"github.com/martonroux/go-study-buddy/internal/aiProvider"
	"github.com/martonroux/go-study-buddy/pkg/ai"
)

const (
	maxPromptLen = 8000
	maxFocusLen  = 500
	maxPDFBytes  = 20 * 1024 * 1024
	maxPDFPages  = 30
	pdfDPI       = 150
)

type generateParams struct {
	SubjectID      int64
	ChapterID      *int64
	Mode           string // prompt | pdf
	Prompt         string
	PDFBytes       []byte
	TargetCount    int    // prompt mode only
	Coverage       string // pdf mode only: essentials | balanced | comprehensive
	Style          string
	Focus          string
	AutoChapters   bool
}

func (h *AIHandler) PostGenerate(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Parse multipart (caps overall body + single file).
	if err := r.ParseMultipartForm(maxPDFBytes + 1024); err != nil {
		http.Error(w, `{"message":"invalid multipart"}`, http.StatusBadRequest)
		return
	}
	gp, err := parseGenerateParams(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Build AI request.
	var messages []ai.AIMessage
	var pages int
	var inputHash string
	var feature = ai.FeaturePrompt

	if gp.Mode == "pdf" {
		feature = ai.FeaturePDF
		imgs, err := aiProvider.ConvertPDFToPNGs(gp.PDFBytes, pdfDPI)
		if err != nil {
			http.Error(w, `{"kind":"pdf_unreadable","message":"couldn't render pdf"}`, http.StatusBadRequest)
			return
		}
		if len(imgs) > maxPDFPages {
			http.Error(w, `{"kind":"pdf_too_large","message":"too many pages"}`, http.StatusRequestEntityTooLarge)
			return
		}
		pages = len(imgs)
		sum := sha256.Sum256(gp.PDFBytes)
		inputHash = hex.EncodeToString(sum[:])

		systemTxt := ai.Render(ai.PromptForFeature(ai.FeaturePDF), renderVars(gp, pages))
		content := []ai.AIContent{{Type: "text", Text: systemTxt}}
		for _, img := range imgs {
			content = append(content, ai.AIContent{
				Type: "image", MimeType: "image/png",
				ImageB64: base64.StdEncoding.EncodeToString(img),
			})
		}
		messages = []ai.AIMessage{{Role: ai.RoleUser, Content: content}}
	} else {
		feature = ai.FeaturePrompt
		sum := sha256.Sum256([]byte(gp.Prompt))
		inputHash = hex.EncodeToString(sum[:])
		systemTxt := ai.Render(ai.PromptForFeature(ai.FeaturePrompt), renderVars(gp, 0))
		messages = []ai.AIMessage{{Role: ai.RoleUser, Content: []ai.AIContent{{Type: "text", Text: systemTxt}}}}
	}

	// SSE setup.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// Launch pipeline.
	subjectID := gp.SubjectID
	req := service.AIRequest{
		UserID:    userID,
		Feature:   feature,
		SubjectID: &subjectID,
		Messages:  messages,
		Schema:    map[string]any{"required": []string{"title", "question", "answer"}},
		MaxTokens: 4096,
		PDFPages:  pages,
		InputHash: inputHash,
	}
	ch, jobID, err := h.AI.RunStructuredGeneration(r.Context(), req)
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}

	// Emit SSE.
	writeEvent(w, flusher, "job", map[string]any{"jobId": jobID})
	chapterIndex := 0

	for chunk := range ch {
		switch chunk.Kind {
		case ai.ChunkProgress:
			writeEvent(w, flusher, "progress", chunk.Progress)
		case ai.ChunkItem:
			// If the card carries a "chapter" (PDF auto-chapter path), map it
			// into a stable index emitted as a separate `chapter` event.
			var card map[string]any
			_ = json.Unmarshal(chunk.Item, &card)
			if gp.AutoChapters {
				if name, ok := card["chapter"].(string); ok && name != "" {
					writeEvent(w, flusher, "chapter", map[string]any{
						"index": chapterIndex, "title": name,
					})
					card["chapterIndex"] = chapterIndex
					chapterIndex++
					delete(card, "chapter")
				}
			} else {
				card["chapterIndex"] = nil
			}
			writeEvent(w, flusher, "card", card)
		case ai.ChunkError:
			writeEvent(w, flusher, "error", chunk.Err)
			return
		case ai.ChunkDone:
			writeEvent(w, flusher, "done", map[string]any{"jobId": jobID})
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, f http.Flusher, name string, data any) {
	b, _ := json.Marshal(data)
	_, _ = io.WriteString(w, "event: "+name+"\n")
	_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
	if f != nil {
		f.Flush()
	}
}

func parseGenerateParams(r *http.Request) (*generateParams, error) {
	subjStr := r.FormValue("subject_id")
	subjID, err := strconv.ParseInt(subjStr, 10, 64)
	if err != nil || subjID <= 0 {
		return nil, fmt.Errorf("subject_id required")
	}
	gp := &generateParams{SubjectID: subjID, Mode: r.FormValue("mode")}
	if ch := r.FormValue("chapter_id"); ch != "" {
		v, err := strconv.ParseInt(ch, 10, 64)
		if err == nil {
			gp.ChapterID = &v
		}
	}
	gp.TargetCount, _ = strconv.Atoi(r.FormValue("target_count"))
	gp.Coverage = r.FormValue("coverage")
	switch gp.Coverage {
	case "essentials", "balanced", "comprehensive":
	default:
		gp.Coverage = "balanced"
	}
	gp.Style = r.FormValue("style")
	if gp.Style == "" {
		gp.Style = "standard"
	}
	gp.Focus = r.FormValue("focus")
	if len(gp.Focus) > maxFocusLen {
		gp.Focus = gp.Focus[:maxFocusLen]
	}
	gp.AutoChapters = r.FormValue("auto_chapters") == "true" && gp.ChapterID == nil && gp.Mode == "pdf"
	switch gp.Mode {
	case "prompt":
		gp.Prompt = r.FormValue("prompt")
		if len(gp.Prompt) == 0 || len(gp.Prompt) > maxPromptLen {
			return nil, fmt.Errorf("prompt length invalid")
		}
	case "pdf":
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("file required")
		}
		defer f.Close()
		if hdr.Size > maxPDFBytes {
			return nil, fmt.Errorf("file too large")
		}
		buf, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		gp.PDFBytes = buf
	default:
		return nil, fmt.Errorf("mode must be prompt or pdf")
	}
	return gp, nil
}

func renderVars(gp *generateParams, pages int) map[string]string {
	tc := "0"
	if gp.TargetCount > 0 {
		tc = strconv.Itoa(gp.TargetCount)
	}
	auto := "false"
	if gp.AutoChapters {
		auto = "true"
	}
	return map[string]string{
		"STYLE":            gp.Style,
		"TARGET_COUNT":     tc,
		"COVERAGE":         gp.Coverage,
		"FOCUS":            gp.Focus,
		"PROMPT":           gp.Prompt,
		"AUTO_CHAPTERS":    auto,
		"PAGE_COUNT":       strconv.Itoa(pages),
	}
}
```

- [ ] **Step 2: Register the route**

In `cmd/app/main.go`:

```go
mux.Handle("POST /ai/generate-flashcards", jwt.RequireVerified(http.HandlerFunc(aiHandler.PostGenerate)))
```

- [ ] **Step 3: Manual smoke test with fake provider**

Temporarily swap the `ClaudeProvider` for a `FakeProvider` in `main.go`, run the server, and `curl`:

```
curl -N -X POST http://localhost:8080/ai/generate-flashcards \
  -H "Authorization: Bearer <verified token>" \
  -F subject_id=1 -F mode=prompt -F prompt="Define eigenvalue"
```

Expected: `event: job` then `event: card` events, then `event: done`.

- [ ] **Step 4: Commit**

```bash
git add api/handler/aiHandler.go cmd/app/main.go
git commit -m "feat(ai): POST /ai/generate-flashcards (SSE)"
```

### Task 7.2: `POST /ai/commit-generation`

**Files:**
- Modify: `api/handler/aiHandler.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add handler**

Append to `api/handler/aiHandler.go`:

```go
type commitReq struct {
	JobID     int64         `json:"job_id"`
	SubjectID int64         `json:"subject_id"`
	Chapters  []commitChap  `json:"chapters"`
	Cards     []commitCard  `json:"cards"`
}
type commitChap struct {
	ClientID string `json:"clientId"`
	Title    string `json:"title"`
}
type commitCard struct {
	ChapterClientID *string `json:"chapterClientId"`
	Title           string  `json:"title"`
	Question        string  `json:"question"`
	Answer          string  `json:"answer"`
}
type commitResp struct {
	SubjectID  int64            `json:"subjectId"`
	ChapterIDs map[string]int64 `json:"chapterIds"`
	CardIDs    []int64          `json:"cardIds"`
}

func (h *AIHandler) PostCommit(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req commitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
		return
	}
	// Editor-access check: reuse whatever helper the flashCardHandler uses.
	// For v1 we check ownership directly (no collaborator table yet in practice).
	if !h.userCanEdit(r.Context(), userID, req.SubjectID) {
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		return
	}

	tx, err := h.AI.DB().BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	chapterIDs := map[string]int64{}
	for _, c := range req.Chapters {
		res, err := tx.ExecContext(r.Context(),
			`INSERT INTO chapters (subject_id, title) VALUES (?, ?)`,
			req.SubjectID, c.Title)
		if err != nil {
			http.Error(w, `{"message":"chapter insert failed"}`, http.StatusInternalServerError)
			return
		}
		id, _ := res.LastInsertId()
		chapterIDs[c.ClientID] = id
	}

	cardIDs := []int64{}
	for _, c := range req.Cards {
		var chapterID *int64
		if c.ChapterClientID != nil {
			if id, ok := chapterIDs[*c.ChapterClientID]; ok {
				chapterID = &id
			}
		}
		res, err := tx.ExecContext(r.Context(),
			`INSERT INTO flashcards (subject_id, chapter_id, title, question, answer, last_result, last_used)
			 VALUES (?, ?, ?, ?, ?, -1, ?)`,
			req.SubjectID, chapterID, c.Title, c.Question, c.Answer,
			time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			http.Error(w, `{"message":"flashcard insert failed"}`, http.StatusInternalServerError)
			return
		}
		id, _ := res.LastInsertId()
		cardIDs = append(cardIDs, id)
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, `{"message":"commit failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(commitResp{
		SubjectID: req.SubjectID, ChapterIDs: chapterIDs, CardIDs: cardIDs,
	})
}

func (h *AIHandler) userCanEdit(ctx context.Context, userID string, subjectID int64) bool {
	var ownerID string
	err := h.AI.DB().QueryRowContext(ctx,
		`SELECT owner_id FROM subjects WHERE id=?`, subjectID).Scan(&ownerID)
	if err != nil {
		return false
	}
	return ownerID == userID
	// TODO: extend when collaborator table lands.
}
```

Add imports: `"time"`, `"context"`.

- [ ] **Step 2: Register the route**

```go
mux.Handle("POST /ai/commit-generation", jwt.RequireVerified(http.HandlerFunc(aiHandler.PostCommit)))
```

- [ ] **Step 3: Commit**

```bash
git add api/handler/aiHandler.go cmd/app/main.go
git commit -m "feat(ai): POST /ai/commit-generation"
```

---

## Phase 8 — Check endpoint

### Task 8.1: `POST /ai/check-flashcard`

**Files:**
- Modify: `api/handler/aiHandler.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add handler**

Append to `api/handler/aiHandler.go`:

```go
type checkReq struct {
	FlashcardID    int64   `json:"flashcard_id"`
	DraftQuestion  *string `json:"draft_question,omitempty"`
	DraftAnswer    *string `json:"draft_answer,omitempty"`
}
type finding struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}
type suggestion struct {
	Title    string `json:"title"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}
type checkResp struct {
	JobID      int64         `json:"jobId"`
	Verdict    string        `json:"verdict"`
	Findings   []finding     `json:"findings"`
	Suggestion suggestion    `json:"suggestion"`
}

func (h *AIHandler) PostCheck(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Short-term rate limit (2 per 10s per user).
	if !checkRateLimiter.Allow(userID) {
		http.Error(w, `{"kind":"provider_rate_limit","message":"too many checks"}`, http.StatusTooManyRequests)
		return
	}

	var req checkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
		return
	}

	var title, question, answer string
	err := h.AI.DB().QueryRowContext(r.Context(),
		`SELECT f.title, f.question, f.answer
		 FROM flashcards f JOIN subjects s ON s.id = f.subject_id
		 WHERE f.id=? AND s.owner_id=?`, req.FlashcardID, userID,
	).Scan(&title, &question, &answer)
	if err != nil {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	if req.DraftQuestion != nil {
		question = *req.DraftQuestion
	}
	if req.DraftAnswer != nil {
		answer = *req.DraftAnswer
	}

	system := ai.Render(ai.PromptForFeature(ai.FeatureCheck), map[string]string{
		"TITLE": title, "QUESTION": question, "ANSWER": answer,
	})
	msgs := []ai.AIMessage{{Role: ai.RoleUser, Content: []ai.AIContent{{Type: "text", Text: system}}}}

	fcID := req.FlashcardID
	ch, jobID, err := h.AI.RunStructuredGeneration(r.Context(), service.AIRequest{
		UserID:      userID,
		Feature:     ai.FeatureCheck,
		FlashcardID: &fcID,
		Messages:    msgs,
		Schema:      nil, // single-object response; we unmarshal directly
		MaxTokens:   1024,
	})
	if err != nil {
		http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
		return
	}

	// The check call is a single-shot object. Consume chunks, accumulate deltas,
	// then JSON-decode at the end.
	var accumulated []byte
	var aiErr *ai.AIError
	for c := range ch {
		switch c.Kind {
		case ai.ChunkItem:
			accumulated = append(accumulated, c.Item...)
		case ai.ChunkError:
			aiErr = c.Err
		}
	}
	if aiErr != nil {
		statusFor := map[string]int{
			"not_entitled":         http.StatusForbidden,
			"quota_exceeded":       http.StatusTooManyRequests,
			"provider_rate_limit":  http.StatusTooManyRequests,
			"provider_timeout":     http.StatusGatewayTimeout,
			"provider_5xx":         http.StatusBadGateway,
			"content_policy":       http.StatusUnprocessableEntity,
			"malformed_output":     http.StatusBadGateway,
		}
		code := http.StatusInternalServerError
		if c, ok := statusFor[aiErr.Kind]; ok {
			code = c
		}
		b, _ := json.Marshal(aiErr)
		http.Error(w, string(b), code)
		return
	}

	// Check prompt returns a single object — not an array — so the pipeline's array parser
	// won't trigger. Switch to direct JSON decode from accumulated deltas.
	var resp checkResp
	resp.JobID = jobID
	if err := json.Unmarshal(accumulated, &resp); err != nil {
		http.Error(w, `{"kind":"malformed_output","message":"bad json"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
```

**Important:** The pipeline's `ArrayStreamParser` expects an array, but `check_flashcard.txt` returns an object. Two ways to handle this:

- (chosen, simpler) Have the check handler bypass the array parser by reading the provider stream directly through a secondary pipeline method.
- (rejected) Force the check prompt to return `[{...}]`.

Since the pipeline only knows arrays, implement a sibling method `RunSingleObjectGeneration` on `AIService` that reuses entitlement/quota/job-row but accumulates raw deltas and emits a single `ChunkItem` with the accumulated bytes. In `aiService.go`:

```go
func (s *AIService) RunSingleObjectGeneration(ctx context.Context, req AIRequest) (<-chan ai.AIChunk, int64, error) {
	// Same pre-flight as RunStructuredGeneration; then run the provider and
	// emit one ChunkItem with the full text and a ChunkDone.
	// (Implementation mirrors RunStructuredGeneration but replaces the parser
	//  loop with a straight accumulator.)
	// ...
}
```

Replace the `h.AI.RunStructuredGeneration` call inside `PostCheck` with `h.AI.RunSingleObjectGeneration`. Implement that method by copying `RunStructuredGeneration`'s pre-flight (entitlement, quota, job row) into a new method and swapping `runAndEmit` for a simpler streaming accumulator.

- [ ] **Step 2: Add the short-term rate limiter**

At the top of `aiHandler.go`:

```go
var checkRateLimiter = newTokenBucket(2, 10*time.Second)

type tokenBucket struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	events map[string][]time.Time
}

func newTokenBucket(limit int, window time.Duration) *tokenBucket {
	return &tokenBucket{limit: limit, window: window, events: map[string][]time.Time{}}
}

func (b *tokenBucket) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-b.window)
	kept := b.events[key][:0]
	for _, t := range b.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= b.limit {
		b.events[key] = kept
		return false
	}
	kept = append(kept, now)
	b.events[key] = kept
	return true
}
```

Add import: `"sync"`.

- [ ] **Step 3: Register the route**

```go
mux.Handle("POST /ai/check-flashcard", jwt.RequireVerified(http.HandlerFunc(aiHandler.PostCheck)))
```

- [ ] **Step 4: Commit**

```bash
git add api/handler/aiHandler.go api/service/aiService.go cmd/app/main.go
git commit -m "feat(ai): POST /ai/check-flashcard with short-term rate limit"
```

---

## Phase 9 — Orphan reaper

### Task 9.1: Reap stuck `running` jobs

**Files:**
- Create: `api/service/aiJobReaper.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Implement the reaper**

Create `api/service/aiJobReaper.go`:

```go
package service

import (
	"context"
	"database/sql"
	"log"
	"time"
)

type AIJobReaper struct {
	db  *sql.DB
	age time.Duration
}

func NewAIJobReaper(db *sql.DB, age time.Duration) *AIJobReaper {
	return &AIJobReaper{db: db, age: age}
}

func (r *AIJobReaper) Start(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := r.ReapOnce(ctx)
				if err != nil {
					log.Printf("ai_job_reaper error: %v", err)
				} else if n > 0 {
					log.Printf("ai_job_reaper: marked %d orphan jobs failed", n)
				}
			}
		}
	}()
}

func (r *AIJobReaper) ReapOnce(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-r.age).UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`UPDATE ai_jobs
		 SET status='failed', error_kind='orphaned', finished_at=?
		 WHERE status='running' AND started_at < ?`,
		time.Now().UTC().Format(time.RFC3339), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 2: Wire it up**

In `cmd/app/main.go`, after constructing the DB:

```go
reaper := service.NewAIJobReaper(db, 1*time.Hour)
ctx, cancelReap := context.WithCancel(context.Background())
reaper.Start(ctx, 15*time.Minute)
defer cancelReap()
```

- [ ] **Step 3: Commit**

```bash
git add api/service/aiJobReaper.go cmd/app/main.go
git commit -m "feat(ai): orphan-job reaper"
```

---

## Phase 10 — Frontend foundation

All remaining tasks are in the frontend repo (`studbud_3/studbud`).

### Task 10.1: Type additions

**Files:**
- Modify: `src/types/models.ts`

- [ ] **Step 1: Append AI-related types**

```ts
// --- AI ---

export type GenerationStyle = 'short' | 'standard' | 'detailed'

export type PdfCoverage = 'essentials' | 'balanced' | 'comprehensive'

export interface GenerationParams {
  mode: 'prompt' | 'pdf'
  subjectId: number
  chapterId: number | null
  prompt?: string
  file?: File | null
  targetCount: number            // 0 = auto. Prompt mode only.
  coverage: PdfCoverage          // PDF mode only.
  style: GenerationStyle
  focus: string
  autoChapters: boolean
}

export interface DraftCard {
  clientId: string
  chapterClientId: string | null
  title: string
  question: string
  answer: string
  edited: boolean
}

export interface DraftChapter {
  clientId: string
  title: string
  order: number
}

export type GenerationStatus = 'idle' | 'streaming' | 'complete' | 'failed' | 'cancelled'

export interface AiErrorShape {
  kind: string
  message: string
}

export interface QuotaCounter {
  used: number
  limit: number
  resetAt: string
}

export interface PdfQuotaCounter extends QuotaCounter {
  pagesUsed: number
  pagesLimit: number
}

export interface AiQuota {
  subscriptionActive: boolean
  prompt: QuotaCounter
  pdf: PdfQuotaCounter
  check: QuotaCounter
}

export interface CheckFinding {
  kind: 'factual' | 'style' | 'typo'
  text: string
}

export interface CheckSuggestion {
  title: string
  question: string
  answer: string
}

export interface CheckResult {
  jobId: number
  verdict: 'ok' | 'minor_issues' | 'major_issues'
  findings: CheckFinding[]
  suggestion: CheckSuggestion
}
```

- [ ] **Step 2: Type check**

Run: `npm run type-check`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add src/types/models.ts
git commit -m "feat(ai): add ai-related types"
```

### Task 10.2: API client

**Files:**
- Create: `src/api/ai.ts`

- [ ] **Step 1: Implement the client**

```ts
import { apiFetch, apiUrl, authHeader } from './client'
import type {
  AiQuota,
  CheckResult,
  GenerationParams,
  AiErrorShape,
} from '@/types/models'

export async function getQuota(): Promise<AiQuota> {
  return apiFetch('/ai/quota')
}

export async function checkFlashcard(args: {
  flashcardId: number
  draftQuestion?: string
  draftAnswer?: string
}): Promise<CheckResult> {
  return apiFetch('/ai/check-flashcard', {
    method: 'POST',
    body: JSON.stringify({
      flashcard_id: args.flashcardId,
      draft_question: args.draftQuestion,
      draft_answer: args.draftAnswer,
    }),
  })
}

export async function commitGeneration(body: {
  jobId: number
  subjectId: number
  chapters: Array<{ clientId: string; title: string }>
  cards: Array<{
    chapterClientId: string | null
    title: string
    question: string
    answer: string
  }>
}): Promise<{
  subjectId: number
  chapterIds: Record<string, number>
  cardIds: number[]
}> {
  return apiFetch('/ai/commit-generation', {
    method: 'POST',
    body: JSON.stringify({
      job_id: body.jobId,
      subject_id: body.subjectId,
      chapters: body.chapters,
      cards: body.cards,
    }),
  })
}

// --- SSE generation client ---

export type SseEvent =
  | { type: 'job'; data: { jobId: number } }
  | { type: 'progress'; data: { phase: string; page?: number; total?: number } }
  | { type: 'chapter'; data: { index: number; title: string } }
  | {
      type: 'card'
      data: {
        title: string
        question: string
        answer: string
        difficulty?: number
        chapterIndex: number | null
      }
    }
  | { type: 'error'; data: AiErrorShape }
  | { type: 'done'; data: { jobId: number } }

export async function generateFlashcards(
  params: GenerationParams,
  signal: AbortSignal,
  onEvent: (e: SseEvent) => void,
): Promise<void> {
  const form = new FormData()
  form.append('subject_id', String(params.subjectId))
  if (params.chapterId !== null) form.append('chapter_id', String(params.chapterId))
  form.append('mode', params.mode)
  if (params.mode === 'prompt') {
    form.append('prompt', params.prompt ?? '')
    form.append('target_count', String(params.targetCount))
  } else {
    if (!params.file) throw new Error('file required')
    form.append('file', params.file)
    form.append('coverage', params.coverage)
  }
  form.append('style', params.style)
  form.append('focus', params.focus)
  form.append('auto_chapters', params.autoChapters ? 'true' : 'false')

  const res = await fetch(apiUrl('/ai/generate-flashcards'), {
    method: 'POST',
    headers: { ...authHeader() },
    body: form,
    signal,
  })
  if (!res.ok || !res.body) {
    const text = await res.text()
    throw new Error(text || `http ${res.status}`)
  }

  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    let idx: number
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const raw = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      const ev = parseSseFrame(raw)
      if (ev) onEvent(ev)
    }
  }
}

function parseSseFrame(raw: string): SseEvent | null {
  let name = ''
  let data = ''
  for (const line of raw.split('\n')) {
    if (line.startsWith('event: ')) name = line.slice(7).trim()
    else if (line.startsWith('data: ')) data += line.slice(6)
  }
  if (!name) return null
  try {
    return { type: name as SseEvent['type'], data: JSON.parse(data) as any }
  } catch {
    return null
  }
}
```

Assumes `src/api/client.ts` exports `apiFetch`, `apiUrl`, and `authHeader`. If it doesn't, inline equivalents; don't restructure existing client code.

- [ ] **Step 2: Type check**

Run: `npm run type-check`
Expected: ok.

- [ ] **Step 3: Commit**

```bash
git add src/api/ai.ts
git commit -m "feat(ai): frontend api client (quota, check, commit, sse)"
```

### Task 10.3: Pinia store

**Files:**
- Create: `src/stores/ai.ts`

- [ ] **Step 1: Implement the store**

```ts
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  checkFlashcard as apiCheckFlashcard,
  commitGeneration,
  generateFlashcards,
  getQuota,
  type SseEvent,
} from '@/api/ai'
import type {
  AiErrorShape,
  AiQuota,
  CheckResult,
  DraftCard,
  DraftChapter,
  GenerationParams,
  GenerationStatus,
} from '@/types/models'

let clientIdCounter = 0
const nextClientId = (prefix: string) => `${prefix}_${++clientIdCounter}_${Date.now()}`

export const useAIStore = defineStore('ai', () => {
  // Quota
  const quota = ref<AiQuota | null>(null)

  async function refreshQuota() {
    try {
      quota.value = await getQuota()
    } catch {
      // quota badge will show stale; not fatal
    }
  }

  // Generation job
  const status = ref<GenerationStatus>('idle')
  const jobId = ref<number | null>(null)
  const subjectId = ref<number | null>(null)
  const chapterId = ref<number | null>(null)
  const params = ref<GenerationParams | null>(null)
  const progress = ref<{ phase: string; page?: number; total?: number } | null>(null)
  const chapters = ref<DraftChapter[]>([])
  const cards = ref<DraftCard[]>([])
  const error = ref<AiErrorShape | null>(null)
  let abortController: AbortController | null = null

  function reset() {
    status.value = 'idle'
    jobId.value = null
    subjectId.value = null
    chapterId.value = null
    params.value = null
    progress.value = null
    chapters.value = []
    cards.value = []
    error.value = null
    abortController?.abort()
    abortController = null
  }

  async function startGeneration(p: GenerationParams) {
    reset()
    params.value = p
    subjectId.value = p.subjectId
    chapterId.value = p.chapterId
    status.value = 'streaming'
    abortController = new AbortController()
    try {
      await generateFlashcards(p, abortController.signal, onEvent)
      if (status.value === 'streaming') status.value = 'complete'
    } catch (e: any) {
      if (abortController?.signal.aborted) {
        status.value = 'cancelled'
      } else {
        status.value = 'failed'
        error.value = { kind: 'internal', message: String(e?.message ?? e) }
      }
    } finally {
      abortController = null
      refreshQuota()
    }
  }

  function onEvent(ev: SseEvent) {
    switch (ev.type) {
      case 'job':
        jobId.value = ev.data.jobId
        return
      case 'progress':
        progress.value = ev.data
        return
      case 'chapter':
        chapters.value.push({
          clientId: nextClientId('ch'),
          title: ev.data.title,
          order: ev.data.index,
        })
        return
      case 'card': {
        const chClientId =
          ev.data.chapterIndex !== null && ev.data.chapterIndex !== undefined
            ? chapters.value[ev.data.chapterIndex]?.clientId ?? null
            : null
        cards.value.push({
          clientId: nextClientId('c'),
          chapterClientId: chClientId,
          title: ev.data.title,
          question: ev.data.question,
          answer: ev.data.answer,
          edited: false,
        })
        return
      }
      case 'error':
        status.value = 'failed'
        error.value = ev.data
        return
      case 'done':
        status.value = 'complete'
        return
    }
  }

  function cancelGeneration() {
    abortController?.abort()
  }

  async function commit(): Promise<{ chapterCount: number; cardCount: number }> {
    if (!jobId.value || !subjectId.value) throw new Error('nothing to commit')
    const res = await commitGeneration({
      jobId: jobId.value,
      subjectId: subjectId.value,
      chapters: chapters.value.map((c) => ({ clientId: c.clientId, title: c.title })),
      cards: cards.value.map((c) => ({
        chapterClientId: c.chapterClientId,
        title: c.title,
        question: c.question,
        answer: c.answer,
      })),
    })
    const out = { chapterCount: chapters.value.length, cardCount: cards.value.length }
    reset()
    refreshQuota()
    return out
  }

  // Check modal state
  const checkOpen = ref(false)
  const checkRunning = ref(false)
  const checkFlashcardId = ref<number | null>(null)
  const checkResult = ref<CheckResult | null>(null)
  const checkError = ref<AiErrorShape | null>(null)
  const applied = ref<{ title: boolean; question: boolean; answer: boolean }>({
    title: false, question: false, answer: false,
  })
  let checkAbort: AbortController | null = null

  async function openCheck(args: {
    flashcardId: number
    draftQuestion?: string
    draftAnswer?: string
  }) {
    checkOpen.value = true
    checkRunning.value = true
    checkFlashcardId.value = args.flashcardId
    checkResult.value = null
    checkError.value = null
    applied.value = { title: false, question: false, answer: false }
    checkAbort = new AbortController()
    try {
      checkResult.value = await apiCheckFlashcard({
        flashcardId: args.flashcardId,
        draftQuestion: args.draftQuestion,
        draftAnswer: args.draftAnswer,
      })
    } catch (e: any) {
      try {
        checkError.value = JSON.parse(e?.message ?? '{}') as AiErrorShape
      } catch {
        checkError.value = { kind: 'internal', message: String(e?.message ?? e) }
      }
    } finally {
      checkRunning.value = false
      refreshQuota()
    }
  }

  function closeCheck() {
    checkAbort?.abort()
    checkOpen.value = false
  }

  return {
    quota,
    refreshQuota,
    status,
    jobId,
    subjectId,
    chapterId,
    params,
    progress,
    chapters,
    cards,
    error,
    startGeneration,
    cancelGeneration,
    commit,
    reset,
    checkOpen,
    checkRunning,
    checkFlashcardId,
    checkResult,
    checkError,
    applied,
    openCheck,
    closeCheck,
  }
})
```

- [ ] **Step 2: Type check**

Run: `npm run type-check`
Expected: ok.

- [ ] **Step 3: Commit**

```bash
git add src/stores/ai.ts
git commit -m "feat(ai): pinia store for generation + check flows"
```

---

## Phase 11 — Small UI components

### Task 11.1: `QuotaBadge.vue`

**Files:**
- Create: `src/components/ai/QuotaBadge.vue`

- [ ] **Step 1: Component**

```vue
<script setup lang="ts">
import { onMounted, computed } from 'vue'
import { useAIStore } from '@/stores/ai'

const ai = useAIStore()
onMounted(() => ai.refreshQuota())

const props = defineProps<{ feature: 'prompt' | 'pdf' | 'check' }>()

const text = computed(() => {
  const q = ai.quota
  if (!q) return ''
  if (props.feature === 'pdf') return `${q.pdf.used}/${q.pdf.limit} PDFs · ${q.pdf.pagesUsed}/${q.pdf.pagesLimit} pages`
  if (props.feature === 'prompt') return `${q.prompt.used}/${q.prompt.limit} generations`
  return `${q.check.used}/${q.check.limit} checks`
})
</script>

<template>
  <span v-if="ai.quota" class="quota-badge">{{ text }}</span>
</template>

<style scoped>
.quota-badge {
  font-size: 10px;
  color: var(--text-muted, #888);
  padding: 4px 8px;
  border: 1px solid #2a2a2d;
  border-radius: 999px;
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add src/components/ai/QuotaBadge.vue
git commit -m "feat(ai): QuotaBadge component"
```

### Task 11.2: `PaywallCard.vue`

**Files:**
- Create: `src/components/ai/PaywallCard.vue`

- [ ] **Step 1: Component**

```vue
<script setup lang="ts">
defineProps<{ title?: string; body?: string }>()
</script>

<template>
  <div class="paywall">
    <h3>{{ title ?? 'AI features are subscription-only' }}</h3>
    <p>{{ body ?? 'Generation and checking will be unlocked with a subscription.' }}</p>
    <button type="button" disabled>Subscribe (coming soon)</button>
  </div>
</template>

<style scoped>
.paywall {
  background: var(--widget-bg, #18181a);
  border-radius: 12px;
  padding: 20px;
  text-align: center;
}
.paywall button { margin-top: 16px; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add src/components/ai/PaywallCard.vue
git commit -m "feat(ai): PaywallCard placeholder"
```

### Task 11.3: `AiGenerationControls.vue`

**Files:**
- Create: `src/components/ai/AiGenerationControls.vue`

- [ ] **Step 1: Component**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { GenerationStyle, PdfCoverage } from '@/types/models'

const props = defineProps<{
  mode: 'prompt' | 'pdf'
  targetCount: number
  coverage: PdfCoverage
  style: GenerationStyle
  focus: string
  autoChapters: boolean
  autoChaptersAvailable: boolean
}>()

const emit = defineEmits<{
  (e: 'update:targetCount', v: number): void
  (e: 'update:coverage', v: PdfCoverage): void
  (e: 'update:style', v: GenerationStyle): void
  (e: 'update:focus', v: string): void
  (e: 'update:autoChapters', v: boolean): void
}>()

const autoCount = computed({
  get: () => props.targetCount === 0,
  set: (v) => emit('update:targetCount', v ? 0 : 10),
})

const coverageOptions: { value: PdfCoverage; label: string; hint: string }[] = [
  { value: 'essentials',    label: 'Essentials',    hint: 'Core ideas only' },
  { value: 'balanced',      label: 'Balanced',      hint: 'Core + key examples' },
  { value: 'comprehensive', label: 'Comprehensive', hint: 'Full coverage of the PDF' },
]

function setStyle(s: GenerationStyle) { emit('update:style', s) }
</script>

<template>
  <div class="controls">
    <!-- Prompt mode: numeric target count (0 = auto) -->
    <template v-if="props.mode === 'prompt'">
      <label>
        <input type="checkbox" v-model="autoCount" /> Auto count
      </label>
      <label v-if="!autoCount">
        Target count
        <input type="number" min="1" max="50"
          :value="props.targetCount"
          @input="e => emit('update:targetCount', Number((e.target as HTMLInputElement).value))"
        />
      </label>
    </template>

    <!-- PDF mode: coverage tiles (card count is derived from PDF content) -->
    <div v-else class="coverage">
      <button v-for="c in coverageOptions"
              :key="c.value"
              :class="{ active: props.coverage === c.value }"
              @click="emit('update:coverage', c.value)">
        <strong>{{ c.label }}</strong>
        <span>{{ c.hint }}</span>
      </button>
      <p class="note">You can trim cards on the next screen.</p>
    </div>

    <div class="style">
      <button v-for="s in (['short','standard','detailed'] as GenerationStyle[])"
              :key="s"
              :class="{ active: props.style === s }"
              @click="setStyle(s)">{{ s }}</button>
    </div>

    <label class="focus">
      Focus
      <textarea maxlength="500"
        :value="props.focus"
        @input="e => emit('update:focus', (e.target as HTMLTextAreaElement).value)"
      />
    </label>

    <label v-if="props.autoChaptersAvailable">
      <input type="checkbox"
        :checked="props.autoChapters"
        @change="e => emit('update:autoChapters', (e.target as HTMLInputElement).checked)" />
      Auto-create chapters from PDF structure
    </label>
  </div>
</template>

<style scoped>
.controls { display: grid; gap: 12px; padding: 16px 0; }
.style button { margin-right: 8px; }
.style button.active { background: var(--primary, #007aff); color: white; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add src/components/ai/AiGenerationControls.vue
git commit -m "feat(ai): generation controls (count, style, focus, auto-chapters)"
```

---

## Phase 12 — Generation pages

### Task 12.1: `FlashcardGeneratePage.vue`

**Files:**
- Create: `src/pages/FlashcardGeneratePage.vue`

- [ ] **Step 1: Page**

```vue
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AiGenerationControls from '@/components/ai/AiGenerationControls.vue'
import QuotaBadge from '@/components/ai/QuotaBadge.vue'
import PaywallCard from '@/components/ai/PaywallCard.vue'
import { useAIStore } from '@/stores/ai'
import type { GenerationStyle, PdfCoverage } from '@/types/models'

const route = useRoute()
const router = useRouter()
const ai = useAIStore()

const subjectId = Number(route.params.subjectId)
const chapterId = route.query.chapter ? Number(route.query.chapter) : null

const mode = ref<'prompt' | 'pdf'>('prompt')
const prompt = ref('')
const file = ref<File | null>(null)
const targetCount = ref(0)
const coverage = ref<PdfCoverage>('balanced')
const style = ref<GenerationStyle>('standard')
const focus = ref('')
const autoChapters = ref(true)

onMounted(() => ai.refreshQuota())

const canGenerate = computed(() => {
  if (!ai.quota) return false
  if (!ai.quota.subscriptionActive) return false
  if (mode.value === 'prompt') return prompt.value.length > 0
  return file.value !== null
})

const quotaOK = computed(() => {
  if (!ai.quota) return true
  return mode.value === 'prompt'
    ? ai.quota.prompt.used < ai.quota.prompt.limit
    : ai.quota.pdf.used < ai.quota.pdf.limit
})

function handleFile(e: Event) {
  const t = e.target as HTMLInputElement
  file.value = t.files?.[0] ?? null
}

async function submit() {
  ai.startGeneration({
    mode: mode.value,
    subjectId,
    chapterId,
    prompt: prompt.value,
    file: file.value,
    targetCount: targetCount.value,
    coverage: coverage.value,
    style: style.value,
    focus: focus.value,
    autoChapters: autoChapters.value && chapterId === null && mode.value === 'pdf',
  })
  router.push({ name: 'flashcard-generate-review', params: { subjectId } })
}
</script>

<template>
  <div class="page">
    <h1>Generate flashcards</h1>

    <PaywallCard v-if="ai.quota && !ai.quota.subscriptionActive" />

    <div v-else class="widget">
      <div class="tabs">
        <button :class="{ active: mode === 'prompt' }" @click="mode = 'prompt'">Prompt</button>
        <button :class="{ active: mode === 'pdf' }" @click="mode = 'pdf'">PDF</button>
      </div>

      <textarea v-if="mode === 'prompt'"
                v-model="prompt"
                maxlength="8000"
                placeholder="Describe what you want flashcards on…" />

      <input v-else type="file" accept="application/pdf" @change="handleFile" />

      <AiGenerationControls
        :mode="mode"
        :targetCount="targetCount" @update:targetCount="v => targetCount = v"
        :coverage="coverage" @update:coverage="v => coverage = v"
        :style="style" @update:style="v => style = v"
        :focus="focus" @update:focus="v => focus = v"
        :autoChapters="autoChapters" @update:autoChapters="v => autoChapters = v"
        :autoChaptersAvailable="chapterId === null && mode === 'pdf'"
      />

      <div class="footer">
        <QuotaBadge :feature="mode === 'pdf' ? 'pdf' : 'prompt'" />
        <button :disabled="!canGenerate || !quotaOK" @click="submit">
          {{ quotaOK ? 'Generate' : 'Quota exceeded' }}
        </button>
      </div>
    </div>
  </div>
</template>
```

- [ ] **Step 2: Commit**

```bash
git add src/pages/FlashcardGeneratePage.vue
git commit -m "feat(ai): FlashcardGeneratePage"
```

### Task 12.2: `FlashcardGenerateReviewPage.vue`

**Files:**
- Create: `src/pages/FlashcardGenerateReviewPage.vue`

- [ ] **Step 1: Page (accordion-style review with inline editing)**

```vue
<script setup lang="ts">
import { ref, computed, onBeforeUnmount, onMounted } from 'vue'
import { useRouter, onBeforeRouteLeave } from 'vue-router'
import MarkdownPreview from '@/components/MarkdownPreview.vue'
import MarkdownToolbar from '@/components/MarkdownToolbar.vue'
import { useAIStore } from '@/stores/ai'

const router = useRouter()
const ai = useAIStore()
const expanded = ref<string | null>(null)

onMounted(() => {
  if (ai.status === 'idle') {
    router.back()
  }
})

const chapterIds = computed(() =>
  Array.from(new Set(ai.cards.map(c => c.chapterClientId))).filter(id => id !== null) as string[]
)

const unassigned = computed(() => ai.cards.filter(c => c.chapterClientId === null))

function chapterFor(id: string) {
  return ai.chapters.find(c => c.clientId === id) ?? null
}
function cardsInChapter(id: string) {
  return ai.cards.filter(c => c.chapterClientId === id)
}

function toggleRow(id: string) {
  expanded.value = expanded.value === id ? null : id
}

function deleteCard(clientId: string) {
  const i = ai.cards.findIndex(c => c.clientId === clientId)
  if (i >= 0) ai.cards.splice(i, 1)
}

function renameChapter(clientId: string, title: string) {
  const ch = ai.chapters.find(c => c.clientId === clientId)
  if (ch) ch.title = title
}

function reassign(cardClientId: string, chapterClientId: string | null) {
  const c = ai.cards.find(c => c.clientId === cardClientId)
  if (c) c.chapterClientId = chapterClientId
}

async function doCommit() {
  const subjectId = ai.subjectId
  try {
    await ai.commit()
    router.replace({ name: 'subject-detail', params: { id: subjectId } })
  } catch (e) {
    alert('Failed to save: ' + String(e))
  }
}

onBeforeRouteLeave((_, __, next) => {
  if (ai.cards.length > 0 && ai.status !== 'idle') {
    if (!confirm(`Discard ${ai.cards.length} generated cards?`)) {
      return next(false)
    }
    ai.reset()
  }
  next()
})

onBeforeUnmount(() => {
  if (ai.status === 'streaming') ai.cancelGeneration()
})
</script>

<template>
  <div class="page">
    <h1>Review generated flashcards</h1>
    <p class="status">
      <span v-if="ai.status === 'streaming'">Generating… {{ ai.cards.length }} so far
        <button @click="ai.cancelGeneration()">Cancel</button></span>
      <span v-else-if="ai.status === 'complete'">Generated {{ ai.cards.length }} cards</span>
      <span v-else-if="ai.status === 'failed'">Stopped at {{ ai.cards.length }} ({{ ai.error?.kind }})</span>
      <span v-else-if="ai.status === 'cancelled'">Cancelled at {{ ai.cards.length }}</span>
    </p>

    <!-- Chapters -->
    <section v-for="id in chapterIds" :key="id" class="chapter">
      <h2>
        <input type="text" :value="chapterFor(id)?.title"
               @input="e => renameChapter(id, (e.target as HTMLInputElement).value)" />
      </h2>
      <div v-for="card in cardsInChapter(id)" :key="card.clientId" class="card-row">
        <header @click="toggleRow(card.clientId)">
          <strong>{{ card.title }}</strong>
          <span class="preview">{{ card.question.slice(0, 80) }}</span>
          <button @click.stop="deleteCard(card.clientId)">✕</button>
        </header>
        <div v-if="expanded === card.clientId" class="editor">
          <label>Title <input v-model="card.title" /></label>
          <label>Chapter
            <select :value="card.chapterClientId" @change="e => reassign(card.clientId, (e.target as HTMLSelectElement).value || null)">
              <option :value="null">(no chapter)</option>
              <option v-for="c in ai.chapters" :key="c.clientId" :value="c.clientId">{{ c.title }}</option>
            </select>
          </label>
          <label>Question</label>
          <MarkdownToolbar v-model="card.question" />
          <MarkdownPreview :value="card.question" />
          <label>Answer</label>
          <MarkdownToolbar v-model="card.answer" />
          <MarkdownPreview :value="card.answer" />
        </div>
      </div>
    </section>

    <!-- Unassigned cards (when auto_chapters off or leftovers) -->
    <section v-if="unassigned.length > 0" class="chapter">
      <h2>Unassigned</h2>
      <div v-for="card in unassigned" :key="card.clientId" class="card-row">
        <header @click="toggleRow(card.clientId)">
          <strong>{{ card.title }}</strong>
          <span class="preview">{{ card.question.slice(0, 80) }}</span>
          <button @click.stop="deleteCard(card.clientId)">✕</button>
        </header>
        <div v-if="expanded === card.clientId" class="editor">
          <label>Title <input v-model="card.title" /></label>
          <label>Question</label>
          <MarkdownToolbar v-model="card.question" />
          <MarkdownPreview :value="card.question" />
          <label>Answer</label>
          <MarkdownToolbar v-model="card.answer" />
          <MarkdownPreview :value="card.answer" />
        </div>
      </div>
    </section>

    <footer class="commit-bar">
      <button @click="ai.reset(); router.back()">Discard</button>
      <button :disabled="ai.cards.length === 0" @click="doCommit">
        Save {{ ai.cards.length }} flashcards
      </button>
    </footer>
  </div>
</template>

<style scoped>
.card-row { border-bottom: 1px solid #2a2a2d; }
.card-row header { display: flex; gap: 8px; padding: 12px; cursor: pointer; align-items: center; }
.card-row .preview { color: #888; flex: 1; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.card-row .editor { padding: 12px; display: grid; gap: 8px; }
.commit-bar { position: sticky; bottom: 0; display: flex; gap: 12px; padding: 16px; background: #090909; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add src/pages/FlashcardGenerateReviewPage.vue
git commit -m "feat(ai): FlashcardGenerateReviewPage with inline-expand editor"
```

### Task 12.3: Router additions

**Files:**
- Modify: `src/router/index.ts`

- [ ] **Step 1: Add routes**

Add alongside existing flashcard routes:

```ts
{
  path: '/subjects/:subjectId/generate',
  name: 'flashcard-generate',
  component: () => import('@/pages/FlashcardGeneratePage.vue'),
  meta: { requiresAuth: true, requiresVerified: true, hideNav: true },
},
{
  path: '/subjects/:subjectId/generate/review',
  name: 'flashcard-generate-review',
  component: () => import('@/pages/FlashcardGenerateReviewPage.vue'),
  meta: { requiresAuth: true, requiresVerified: true, hideNav: true },
},
```

- [ ] **Step 2: Type check + smoke boot**

Run: `npm run type-check` then `npm run dev` and browse to `/#/subjects/1/generate` to verify the route loads.

- [ ] **Step 3: Commit**

```bash
git add src/router/index.ts
git commit -m "feat(ai): routes for generate + review pages"
```

---

## Phase 13 — AI Check modal

### Task 13.1: `AiCheckModal.vue`

**Files:**
- Create: `src/components/ai/AiCheckModal.vue`

- [ ] **Step 1: Component**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { useAIStore } from '@/stores/ai'
import { useFlashcardDraft } from '@/stores/flashcardDraft'

const ai = useAIStore()
const draft = useFlashcardDraft()

const verdictClass = computed(() => {
  if (!ai.checkResult) return ''
  return `verdict-${ai.checkResult.verdict}`
})

function applyTitle() {
  if (!ai.checkResult) return
  draft.title = ai.checkResult.suggestion.title
  ai.applied.title = true
}
function applyQuestion() {
  if (!ai.checkResult) return
  draft.question = ai.checkResult.suggestion.question
  ai.applied.question = true
}
function applyAnswer() {
  if (!ai.checkResult) return
  draft.answer = ai.checkResult.suggestion.answer
  ai.applied.answer = true
}
function applyAll() { applyTitle(); applyQuestion(); applyAnswer() }
</script>

<template>
  <div v-if="ai.checkOpen" class="modal-backdrop" @click.self="ai.closeCheck()">
    <div class="modal">
      <header><h2>AI check</h2><button @click="ai.closeCheck()">✕</button></header>

      <div v-if="ai.checkRunning" class="loading">Checking this flashcard…</div>

      <div v-else-if="ai.checkError" class="error">
        <p><strong>{{ ai.checkError.kind }}</strong>: {{ ai.checkError.message }}</p>
        <button @click="ai.closeCheck()">Close</button>
      </div>

      <div v-else-if="ai.checkResult" class="result">
        <div class="banner" :class="verdictClass">
          <span v-if="ai.checkResult.verdict === 'ok'">Looks good.</span>
          <span v-else-if="ai.checkResult.verdict === 'minor_issues'">A few small issues.</span>
          <span v-else>Significant issues found.</span>
        </div>

        <ul v-if="ai.checkResult.findings.length > 0" class="findings">
          <li v-for="(f, i) in ai.checkResult.findings" :key="i">
            <span class="tag">[{{ f.kind }}]</span> {{ f.text }}
          </li>
        </ul>

        <section class="diff">
          <div class="row">
            <div class="col">
              <label>Title (current)</label>
              <div>{{ draft.title }}</div>
            </div>
            <div class="col">
              <label>Title (suggested)</label>
              <div>{{ ai.checkResult.suggestion.title }}</div>
              <button :disabled="ai.applied.title" @click="applyTitle">
                {{ ai.applied.title ? 'Applied ✓' : 'Apply title' }}
              </button>
            </div>
          </div>
          <div class="row">
            <div class="col">
              <label>Question (current)</label>
              <pre>{{ draft.question }}</pre>
            </div>
            <div class="col">
              <label>Question (suggested)</label>
              <pre>{{ ai.checkResult.suggestion.question }}</pre>
              <button :disabled="ai.applied.question" @click="applyQuestion">
                {{ ai.applied.question ? 'Applied ✓' : 'Apply question' }}
              </button>
            </div>
          </div>
          <div class="row">
            <div class="col">
              <label>Answer (current)</label>
              <pre>{{ draft.answer }}</pre>
            </div>
            <div class="col">
              <label>Answer (suggested)</label>
              <pre>{{ ai.checkResult.suggestion.answer }}</pre>
              <button :disabled="ai.applied.answer" @click="applyAnswer">
                {{ ai.applied.answer ? 'Applied ✓' : 'Apply answer' }}
              </button>
            </div>
          </div>
        </section>

        <footer>
          <button @click="ai.closeCheck()">Dismiss</button>
          <button @click="applyAll">Apply all</button>
        </footer>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.6); z-index: 1000; display: grid; place-items: center; }
.modal { width: min(800px, 95vw); max-height: 90vh; overflow: auto; background: #18181a; border-radius: 12px; padding: 20px; }
.banner { padding: 12px; border-radius: 8px; margin: 12px 0; }
.verdict-ok { background: #0f3d22; }
.verdict-minor_issues { background: #3d2f0f; }
.verdict-major_issues { background: #3d0f0f; }
.row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin: 16px 0; }
@media (max-width: 640px) { .row { grid-template-columns: 1fr; } }
pre { white-space: pre-wrap; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add src/components/ai/AiCheckModal.vue
git commit -m "feat(ai): AiCheckModal with per-field apply"
```

---

## Phase 14 — Entry-point wiring

### Task 14.1: "Generate with AI" on subject detail

**Files:**
- Modify: `src/pages/SubjectDetailPage.vue`

- [ ] **Step 1: Locate the action buttons block**

Find where the "Create Flashcard" / "Start Training" buttons live.

- [ ] **Step 2: Add the Generate button**

Add near those buttons:

```vue
<router-link
  v-if="canEdit"
  :to="{ name: 'flashcard-generate', params: { subjectId: subject.id } }"
  class="btn btn-ai"
>
  Generate with AI
</router-link>
```

Use the existing `canEdit` flag (editor-access check). If none exists, check `subject.accessLevel === 'owner' || subject.accessLevel === 'editor'`.

- [ ] **Step 3: Commit**

```bash
git add src/pages/SubjectDetailPage.vue
git commit -m "feat(ai): wire 'Generate with AI' on subject detail"
```

### Task 14.2: "Generate with AI" on chapter detail

**Files:**
- Modify: `src/pages/ChapterDetailPage.vue`

- [ ] **Step 1: Add the button with chapter context**

```vue
<router-link
  v-if="canEdit"
  :to="{ name: 'flashcard-generate', params: { subjectId: chapter.subjectId }, query: { chapter: chapter.id } }"
  class="btn btn-ai"
>
  Generate with AI
</router-link>
```

- [ ] **Step 2: Commit**

```bash
git add src/pages/ChapterDetailPage.vue
git commit -m "feat(ai): wire 'Generate with AI' on chapter detail"
```

### Task 14.3: "Check with AI" button in the flashcard editor + modal mount

**Files:**
- Modify: `src/pages/FlashcardCreatePage.vue`
- Modify: `src/pages/FlashcardEditPage.vue`
- Modify: `src/App.vue` (mount the modal globally)

- [ ] **Step 1: Mount the modal globally**

In `src/App.vue`, add near the top of `<template>` (above the router-view):

```vue
<AiCheckModal />
```

And import:

```vue
<script setup lang="ts">
import AiCheckModal from '@/components/ai/AiCheckModal.vue'
</script>
```

- [ ] **Step 2: Add the check button to the editor pages**

In both `FlashcardCreatePage.vue` and `FlashcardEditPage.vue`, near Save/Delete:

```vue
<button type="button"
        :disabled="!canCheck"
        @click="ai.openCheck({ flashcardId: flashcardId, draftQuestion: draft.question, draftAnswer: draft.answer })">
  Check with AI
</button>
```

With:

```ts
import { useAIStore } from '@/stores/ai'
import { computed } from 'vue'
const ai = useAIStore()
const canCheck = computed(() => {
  if (!ai.quota) return false
  if (!ai.quota.subscriptionActive) return false
  if (ai.quota.check.used >= ai.quota.check.limit) return false
  return draft.title.length > 0 || draft.question.length > 0
})
```

For `FlashcardCreatePage.vue`, the `flashcardId` is `null` until the first save — disable the button there with a tooltip "Save the flashcard first to use AI check." (The check endpoint needs a real flashcard id.)

- [ ] **Step 3: Type check + smoke test**

Run: `npm run type-check`
Expected: ok.

- [ ] **Step 4: Commit**

```bash
git add src/App.vue src/pages/FlashcardCreatePage.vue src/pages/FlashcardEditPage.vue
git commit -m "feat(ai): 'Check with AI' button + global modal mount"
```

---

## Phase 15 — Manual QA & documentation

### Task 15.1: Run the full QA checklist

No code changes — execution of the checklist from the design doc (Section 9.2). This task exists so the engineer has a concrete deliverable for the QA gate.

- [ ] **Step 1: Grant yourself entitlement**

```bash
ADMIN_API_ENABLED=true go run ./cmd/app
curl -X POST http://localhost:8080/admin/set-ai-subscription \
  -H 'content-type: application/json' \
  -d '{"user_id":"<your user id>","active":true}'
```

- [ ] **Step 2: Walk each checklist item from Section 9.2 of the design doc**

For each numbered item (1-10), exercise the flow in the browser (`npm run dev` in the frontend repo) and verify the expected behavior. If any item fails, open a follow-up task in the plan with the bug description.

- [ ] **Step 3: Save reference prompts + outputs**

Create `docs/superpowers/ai-evals/` (frontend repo) and commit the 5 prompts + 3 PDFs + the outputs you used for the quality check.

- [ ] **Step 4: Commit the evals**

```bash
git add docs/superpowers/ai-evals/
git commit -m "docs(ai): reference prompts + generated outputs for manual eval"
```

---

## Self-Review

**Spec coverage.** Walking each spec section against the plan:

- §3 Architecture — Tasks 1.1, 3.1, 4.2, 6.x, 7.x, 8.1 map directly. ✓
- §4 Pipeline primitive — Tasks 4.1–4.4. ✓ (with `RunSingleObjectGeneration` variant added in 8.1 for the check endpoint because it returns an object, not an array)
- §5 Data model — Task 1.1 covers all three schema changes. ✓
- §6 API surface — `/ai/quota` (6.1), `/admin/set-ai-subscription` (6.2), `/ai/generate-flashcards` (7.1), `/ai/commit-generation` (7.2), `/ai/check-flashcard` (8.1). ✓
- §7 Frontend flows — Tasks 10.x, 11.x, 12.x, 13.1, 14.x. ✓
- §8 Errors & safety — Error taxonomy in `classifyProviderErr` (Task 4.3), partial-result contract in `runAndEmit` (Task 4.3), short-term rate limit in Task 8.1, concurrent-generation cap in Task 4.2. PDF safety caps in Task 7.1. ✓
- §9 Testing — Tasks 2.1, 4.1, 4.4 for service units; Task 15.1 for manual QA. No HTTP integration tests (`handlers_test.go`) are written in this plan — the spec called them "thin" and optional. If you want them, add a Phase 7.5 later.
- §10/11 Out of scope / non-goals — plan stays within scope. ✓

**Placeholders.** Scanned for TBD/TODO:
- One intentional `// TODO: extend when collaborator table lands` in `userCanEdit`. This is a non-critical deferred hook, not a blocker.
- `runAndEmit` "placeholder" in Task 4.2 is replaced in Task 4.3. Intentional two-step. ✓

**Type consistency.**
- `FeatureKey` values match across backend (`ai.FeaturePrompt`/`PDF`/`Check`) and the `feature` column in `ai_jobs`. ✓
- `AIRequest` fields used in `RunStructuredGeneration` tests match the struct definition. ✓
- Frontend `GenerationParams` fields match the form-data keys the backend parser expects. ✓
- `QuotaSnapshot` maps field-for-field to `quotaResp`. ✓

**Known soft spots (resolved inline or flagged):**
- `RunSingleObjectGeneration` mentioned in Task 8.1 needs to be an actual method on `AIService` — Task 8.1 Step 1 instructs implementing it by copying the pre-flight from `RunStructuredGeneration`. Explicit enough.
- Access control check (`userCanEdit`) is owner-only in the plan; collaborator support is left for a later spec and clearly annotated.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-18-ai-flashcard-generation.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for a plan this size because each task has a clean, scoped deliverable and the reviews catch drift early.

**2. Inline Execution** — execute tasks in this session using executing-plans, with batch checkpoints for review. Lower ceremony, but long-running sessions tend to drift context.

**Which approach?**
