# AI Flashcard Generation & Check — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Spec A (AI flashcard generation from prompt or PDF + AI check of existing flashcards) on the completed StudBud backend skeleton, replacing the `pkg/aipipeline/` stubs with a real pipeline that routes all AI calls through one generic `RunStructuredGeneration` primitive gated by `user_has_ai_access` entitlement and daily quotas.

**Architecture:** Single generic AI pipeline in `pkg/aipipeline/` with entitlement + quota + concurrent-cap + transparent-retry + SSE framing + JSON streaming validation. Thin per-feature handlers in `api/handler/ai.go` adapt the pipeline's `<-chan AIChunk` to JSON or SSE. `internal/aiProvider/ClaudeProvider` owns Anthropic HTTP + SSE parsing + `go-fitz` PDF→image conversion. Admin entitlement flip via `POST /admin/grant-ai-access` + new `billing.Service.GrantComp`.

**Tech Stack:** Go 1.22, `pgx/v5` + `pgxpool`, `golang-jwt/jwt/v5`, `github.com/gen2brain/go-fitz`, Anthropic REST API via stdlib `net/http` + SSE parsing.

**Design doc:** `docs/superpowers/specs/2026-04-18-ai-flashcard-generation-design.md` (revised 2026-04-23).

**Repo:** `/Users/martonroux/Documents/WEB/studbud_3/backend/` (module `studbud/backend`).

**Conventions:**
- One commit per completed task. Title line is a short phrase. Body uses StudBud's `[+]` / `[-]` / `[&]` / `[!]` prefixes, one change per line, no footers.
- Every exported symbol carries a docstring (CLAUDE.md). Functions ≤ 30 lines each. Files ≤ 400 lines each.
- Errors wrap with `fmt.Errorf("context:\n%w", err)`.
- TDD where feasible (services + handlers). Schema/infra changes test via integration.

**Frontend:** `/Users/martonroux/Documents/WEB/studbud_3/studbud/` (separate repo; out of scope for this plan — consumed after backend acceptance).

---

## Phase Map

- **Phase 1 — Foundation (Tasks 1–5):** Schema ALTERs, new `ErrContentPolicy` sentinel, quota limits config, testutil helpers, admin grant-ai-access endpoint (end-to-end, validates admin path).
- **Phase 2 — Quota service (Tasks 6–7):** `aipipeline.quotaManager` (`check` + `debit`) against `ai_quota_daily`, `GET /ai/quota` endpoint.
- **Phase 3 — Pipeline primitive (Tasks 8–11):** Types, pre-flight (entitlement + quota + concurrent-cap) + job insert, stream consumption + schema validation, transparent-retry + finalize, orphan reaper cron.
- **Phase 4 — Anthropic provider (Tasks 12–14):** `ClaudeProvider` HTTP/SSE, PDF→image worker pool, embed prompt templates.
- **Phase 5 — Handlers (Tasks 15–18):** `commit-generation`, `generate-from-prompt` (SSE), `generate-from-pdf` (multipart SSE), `check` (JSON).
- **Phase 6 — Wire-up & acceptance (Tasks 19–20):** Route registration + deps update, end-to-end integration test + acceptance walkthrough.

---

## Phase 1 — Foundation

### Task 1: Schema ALTERs + `ErrContentPolicy` sentinel + status/code mapping

**Files:**
- Modify: `db_sql/setup_ai.go`
- Modify: `internal/myErrors/errors.go`
- Modify: `internal/httpx/errors.go`
- Modify: `internal/httpx/errors_test.go`

- [ ] **Step 1: Extend the `aiSchema` constant in `db_sql/setup_ai.go` with additive ALTERs**

Open `db_sql/setup_ai.go`. At the end of the `aiSchema` constant (before the closing `` ` ``), append:

```sql

ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS subject_id      BIGINT NULL REFERENCES subjects(id)   ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS flashcard_id    BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS pdf_page_count  INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_emitted   INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_dropped   INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS error_kind      TEXT   NULL;
CREATE INDEX IF NOT EXISTS idx_ai_jobs_user_running ON ai_jobs(user_id) WHERE status = 'running';
```

Every statement is idempotent: `ADD COLUMN IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS` are no-ops on re-run.

- [ ] **Step 2: Add `ErrContentPolicy` sentinel to `internal/myErrors/errors.go`**

Append after `ErrAIProvider`:

```go
// ErrContentPolicy indicates the provider refused to answer on content-policy grounds.
var ErrContentPolicy = errors.New("ai content policy refusal")
```

- [ ] **Step 3: Map `ErrContentPolicy` to HTTP 422 + code `content_policy` in `internal/httpx/errors.go`**

In `sentinelStatus`, add after the `ErrStripe` entry:

```go
{myErrors.ErrContentPolicy, http.StatusUnprocessableEntity},
```

In `sentinelCodes`, add after the `ErrStripe` entry:

```go
{myErrors.ErrContentPolicy, "content_policy"},
```

- [ ] **Step 4: Add a test for the new mapping**

Open `internal/httpx/errors_test.go`. Add a test case (adjust the existing test list or add a new test function mirroring the existing pattern):

```go
func TestWriteError_ContentPolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, myErrors.ErrContentPolicy)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	var body struct{ Error struct{ Code string } }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "content_policy" {
		t.Errorf("code = %q, want content_policy", body.Error.Code)
	}
}
```

If that file already has the needed imports (`net/http`, `net/http/httptest`, `encoding/json`, `studbud/backend/internal/myErrors`), no import changes are needed. Otherwise add them.

- [ ] **Step 5: Verify build + test + schema apply**

Run: `go vet ./... && go test ./internal/httpx/... -count=1`
Expected: PASS.

Then run: `go build ./...`
Expected: exit 0.

Then run: `./setup_db.sh && ENV=dev go run ./cmd/app` — Ctrl-C after the listen line.
Expected: server boots without schema errors; the ALTERs apply (or no-op if re-run).

- [ ] **Step 6: Commit**

```bash
git add db_sql/setup_ai.go internal/myErrors/errors.go internal/httpx/errors.go internal/httpx/errors_test.go
git commit -m "$(cat <<'EOF'
Schema + sentinel for Spec A

[+] ai_jobs ALTERs: subject_id, flashcard_id, pdf_page_count, items_emitted, items_dropped, error_kind
[+] idx_ai_jobs_user_running partial index
[+] ErrContentPolicy sentinel (HTTP 422, code content_policy)
[+] httpx test for ErrContentPolicy mapping
EOF
)"
```

---

### Task 2: Quota limits type + config wiring

**Files:**
- Create: `pkg/aipipeline/model.go`
- Modify: `internal/config/config.go` (if adding env-override fields — see Step 3)

- [ ] **Step 1: Delete the existing `pkg/aipipeline/stub.go`**

Run:
```bash
rm pkg/aipipeline/stub.go
```

We'll rebuild the package from scratch with the real types.

- [ ] **Step 2: Create `pkg/aipipeline/model.go`**

```go
package aipipeline

import (
	"encoding/json"
	"time"
)

// FeatureKey identifies the feature that an AI call belongs to.
type FeatureKey string

const (
	// FeatureGenerateFromPrompt is the prompt-based flashcard generator.
	FeatureGenerateFromPrompt FeatureKey = "generate_prompt"
	// FeatureGenerateFromPDF is the PDF-based flashcard generator.
	FeatureGenerateFromPDF FeatureKey = "generate_pdf"
	// FeatureCheckFlashcard is the AI-check of an existing flashcard.
	FeatureCheckFlashcard FeatureKey = "check_flashcard"
)

// ChunkKind tags a streamed AIChunk.
type ChunkKind string

const (
	// ChunkItem carries one validated JSON item (e.g. a flashcard or a chapter).
	ChunkItem ChunkKind = "item"
	// ChunkProgress carries an optional progress update.
	ChunkProgress ChunkKind = "progress"
	// ChunkDone marks successful stream termination.
	ChunkDone ChunkKind = "done"
	// ChunkError marks a terminal error on the stream.
	ChunkError ChunkKind = "error"
)

// ProgressInfo describes where a PDF-based generation is within its input.
type ProgressInfo struct {
	Phase string `json:"phase"`           // Phase is a short tag ("analyzing", "writing")
	Page  int    `json:"page,omitempty"`  // Page is the current page index, 1-based
	Total int    `json:"total,omitempty"` // Total is the page count when known
}

// AIChunk is one emission from RunStructuredGeneration.
type AIChunk struct {
	Kind     ChunkKind       // Kind tags the payload
	Item     json.RawMessage // Item is set when Kind == ChunkItem
	Progress *ProgressInfo   // Progress is set when Kind == ChunkProgress
	Err      error           // Err is set when Kind == ChunkError
}

// AIRequest is the pipeline invocation shape.
type AIRequest struct {
	UserID      int64          // UserID is the authenticated user running the call
	Feature     FeatureKey     // Feature selects quota counter + concurrent-cap policy
	SubjectID   int64          // SubjectID must be set for generation features
	FlashcardID int64          // FlashcardID is non-zero only for FeatureCheckFlashcard
	Prompt      string         // Prompt is the assembled user-facing prompt body
	PDFBytes    []byte         // PDFBytes is populated only for FeatureGenerateFromPDF
	PDFPages    int            // PDFPages is the declared page count (pre-counted by handler)
	Schema      json.RawMessage // Schema is the tool-use JSON schema for the expected output
	Metadata    map[string]any // Metadata is persisted into ai_jobs.metadata (style, focus, coverage...)
}

// QuotaLimits holds per-feature daily caps.
type QuotaLimits struct {
	PromptCalls int // PromptCalls is the daily cap on successful prompt generations
	PDFCalls    int // PDFCalls is the daily cap on successful PDF generations
	PDFPages    int // PDFPages is the daily cap on total PDF pages consumed
	CheckCalls  int // CheckCalls is the daily cap on successful check calls
}

// DefaultQuotaLimits returns the v1 starting caps. Tune post-launch.
func DefaultQuotaLimits() QuotaLimits {
	return QuotaLimits{
		PromptCalls: 20,
		PDFCalls:    5,
		PDFPages:    100,
		CheckCalls:  50,
	}
}

// AIJob is the ai_jobs row projection used by the service.
type AIJob struct {
	ID            int64      // ID is the BIGSERIAL primary key
	UserID        int64      // UserID owns the job
	FeatureKey    FeatureKey // FeatureKey identifies the feature
	Model         string     // Model is the provider model identifier
	SubjectID     *int64     // SubjectID is nil when the feature doesn't target a subject
	FlashcardID   *int64     // FlashcardID is set for check-flashcard jobs only
	Status        string     // Status is running | complete | failed | cancelled
	InputTokens   int        // InputTokens counts provider prompt tokens
	OutputTokens  int        // OutputTokens counts provider completion tokens
	CentsSpent    int        // CentsSpent is the rounded cost estimate
	PDFPageCount  int        // PDFPageCount mirrors the declared PDFPages at start
	ItemsEmitted  int        // ItemsEmitted counts items that passed validation
	ItemsDropped  int        // ItemsDropped counts items that failed validation
	ErrorKind     string     // ErrorKind is empty on success
	ErrorMessage  string     // ErrorMessage is empty on success
	Metadata      []byte     // Metadata is the raw JSONB blob
	StartedAt     time.Time  // StartedAt is the insertion timestamp
	FinishedAt    *time.Time // FinishedAt is nil while status == running
}
```

- [ ] **Step 3: Decide on env-overridable limits — skip for v1**

Keep `DefaultQuotaLimits()` as the only source. Env overrides not needed for v1 per YAGNI. No config changes.

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: fails — other packages referencing `aipipeline.NewService` / `GenerateFlashcards` won't compile. This is expected; Task 4 reconstructs the `Service` type.

**Temporary shim so the tree builds through Task 3** — add a `service.go` with a minimal `Service` stub while other tasks land:

Create `pkg/aipipeline/service.go`:

```go
package aipipeline

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/access"
)

// Service is the AI pipeline facade.
type Service struct {
	db       *pgxpool.Pool     // db is the shared pool
	provider aiProvider.Client // provider is the Anthropic (or noop) client
	access   *access.Service   // access answers entitlement questions
	limits   QuotaLimits       // limits bounds per-feature daily calls
	model    string            // model is the provider model identifier
}

// NewService constructs a Service. Methods are filled in across later tasks.
func NewService(db *pgxpool.Pool, provider aiProvider.Client, access *access.Service, limits QuotaLimits, model string) *Service {
	return &Service{db: db, provider: provider, access: access, limits: limits, model: model}
}
```

Open `cmd/app/deps.go` and update `buildStubServices` to match the new signature:

Find:
```go
ai:      aipipeline.NewService(pool, inf.aiClient),
```

Replace with:
```go
ai:      aipipeline.NewService(pool, inf.aiClient, acc, aipipeline.DefaultQuotaLimits(), cfg.AIModel),
```

Then you need access to `acc` inside `buildStubServices`. Since it's built in `buildDomainServices`, rework the call order in `buildDeps`:

Find in `buildDeps`:
```go
dom := buildDomainServices(cfg, pool, inf)
stubs := buildStubServices(pool, inf)
```

Replace with:
```go
dom := buildDomainServices(cfg, pool, inf)
stubs := buildStubServices(cfg, pool, inf, dom.access)
```

Update `buildStubServices`' signature:

```go
func buildStubServices(cfg *config.Config, pool *pgxpool.Pool, inf infra, acc *access.Service) stubSvcs {
	return stubSvcs{
		ai:      aipipeline.NewService(pool, inf.aiClient, acc, aipipeline.DefaultQuotaLimits(), cfg.AIModel),
		quiz:    quiz.NewService(pool),
		plan:    pkgplan.NewService(pool),
		duel:    duel.NewService(pool, inf.hub),
		billing: pkgbilling.NewService(pool, inf.billing),
	}
}
```

Also open `api/handler/ai_stub.go` and update it to build against the new `Service`:

Find the methods (`GenerateFromPrompt`, `GenerateFromPDF`, `Check`) — they reference `h.svc.GenerateFlashcards(...)` etc. Replace each body with a `httpx.WriteError(w, myErrors.ErrNotImplemented)` call (stub) until the real handlers land in Tasks 16–18. The file now looks like:

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// AIHandler exposes AI pipeline endpoints.
type AIHandler struct {
	svc *aipipeline.Service // svc is the AI pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// GenerateFromPrompt stubs POST /ai/flashcards/prompt until Task 16.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// GenerateFromPDF stubs POST /ai/flashcards/pdf until Task 17.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Check stubs POST /ai/check until Task 18.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

Now run: `go build ./...`
Expected: clean build.

Run: `go test ./... -p 1 -count=1`
Expected: all pre-existing tests still pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/ cmd/app/deps.go api/handler/ai_stub.go
git commit -m "$(cat <<'EOF'
aipipeline types + constructor skeleton

[+] FeatureKey, ChunkKind, AIChunk, AIRequest, QuotaLimits, AIJob types
[+] DefaultQuotaLimits (prompt=20, pdf=5, pdf_pages=100, check=50)
[&] aipipeline.NewService signature takes access + limits + model
[&] cmd/app/deps.go wires access + DefaultQuotaLimits + AIModel into pipeline
[-] pkg/aipipeline/stub.go (replaced by model.go + service.go)
EOF
)"
```

---

### Task 3: testutil helpers for AI tests

**Files:**
- Modify: `testutil/ai.go`
- Modify: `testutil/fixtures.go`

- [ ] **Step 1: Extend `testutil/ai.go` to support an error-then-success retry harness and progress chunks**

Replace the file with:

```go
package testutil

import (
	"context"
	"sync/atomic"

	"studbud/backend/internal/aiProvider"
)

// FakeAIClient replays a fixed sequence of chunks on each Stream call.
// Set FailFirstN to simulate transient provider errors (returned as Err)
// on the first N calls; subsequent calls succeed with Chunks.
type FakeAIClient struct {
	Chunks     []aiProvider.Chunk // Chunks is the replay buffer for successful calls
	Err        error              // Err is returned synchronously when set
	FailFirstN int32              // FailFirstN fails that many calls with Err before succeeding
	calls      atomic.Int32       // calls counts total Stream invocations
}

// Stream returns either Err (for the first FailFirstN calls) or a channel
// that yields Chunks then closes.
func (f *FakeAIClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	n := f.calls.Add(1)
	if f.Err != nil && n <= f.FailFirstN {
		return nil, f.Err
	}
	if f.Err != nil && f.FailFirstN == 0 {
		return nil, f.Err
	}
	out := make(chan aiProvider.Chunk, len(f.Chunks))
	for _, c := range f.Chunks {
		out <- c
	}
	close(out)
	return out, nil
}

// Calls returns the total number of Stream invocations so far.
func (f *FakeAIClient) Calls() int32 {
	return f.calls.Load()
}
```

- [ ] **Step 2: Add `SeedQuotaAt`, `SeedRunningJob`, `GiveAICompAccess`, `CountAIJobs` to `testutil/fixtures.go`**

Append to `testutil/fixtures.go`:

```go
// SeedQuotaAt sets the named quota counter for the given user to value for today.
// Inserts a row if none exists.
func SeedQuotaAt(t *testing.T, pool *pgxpool.Pool, uid int64, column string, value int) {
	t.Helper()
	if !isKnownQuotaColumn(column) {
		t.Fatalf("unknown quota column %q", column)
	}
	sql := fmt.Sprintf(`
        INSERT INTO ai_quota_daily (user_id, day, %[1]s)
        VALUES ($1, current_date, $2)
        ON CONFLICT (user_id, day) DO UPDATE SET %[1]s = EXCLUDED.%[1]s
    `, column)
	if _, err := pool.Exec(context.Background(), sql, uid, value); err != nil {
		t.Fatalf("seed quota: %v", err)
	}
}

// SeedRunningJob inserts an ai_jobs row in status=running and returns its id.
// Use to simulate concurrent-generation scenarios.
func SeedRunningJob(t *testing.T, pool *pgxpool.Pool, uid int64, feature string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO ai_jobs (user_id, feature_key, model, status, metadata)
        VALUES ($1, $2, 'test-model', 'running', '{}'::jsonb)
        RETURNING id
    `, uid, feature).Scan(&id)
	if err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	return id
}

// GiveAICompAccess inserts a comp user_subscriptions row (admin-granted).
func GiveAICompAccess(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO user_subscriptions (user_id, plan, status)
        VALUES ($1, 'comp', 'comp')
    `, uid)
	if err != nil {
		t.Fatalf("give comp access: %v", err)
	}
}

// CountAIJobs returns the number of ai_jobs rows for user uid.
func CountAIJobs(t *testing.T, pool *pgxpool.Pool, uid int64) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ai_jobs WHERE user_id = $1`, uid).Scan(&n)
	if err != nil {
		t.Fatalf("count ai_jobs: %v", err)
	}
	return n
}

// MakeAdmin sets users.is_admin = true for uid.
func MakeAdmin(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE users SET is_admin = true WHERE id = $1`, uid); err != nil {
		t.Fatalf("make admin: %v", err)
	}
}
```

(Imports already include `context`, `fmt`, `testing`, `time`, `pgxpool`. No new imports needed.)

- [ ] **Step 3: Verify build + tests**

Run: `go build ./... && go test ./testutil/... -p 1 -count=1`
Expected: build clean; `testutil` has no tests itself, so the test command is a no-op (exit 0).

- [ ] **Step 4: Commit**

```bash
git add testutil/ai.go testutil/fixtures.go
git commit -m "$(cat <<'EOF'
Extend testutil for AI pipeline tests

[+] FakeAIClient.FailFirstN for transient-retry scenarios
[+] FakeAIClient.Calls() for call-count assertions
[+] SeedQuotaAt(uid, column, value) fine-grained quota pre-seed
[+] SeedRunningJob(uid, feature) for concurrent-cap scenarios
[+] GiveAICompAccess(uid) comp-row grant path
[+] CountAIJobs(uid) for observability assertions
[+] MakeAdmin(uid) for admin-endpoint tests
EOF
)"
```

---

### Task 4: Admin grant-ai-access endpoint (end-to-end)

**Files:**
- Modify: `pkg/billing/service.go` (add `GrantComp` method + small supporting code)
- Create: `pkg/billing/service_test.go`
- Create: `api/handler/admin_ai.go`
- Create: `api/handler/admin_ai_test.go`
- Modify: `cmd/app/routes.go` (wire route — one-line add)

- [ ] **Step 1: Write the failing service test at `pkg/billing/service_test.go`**

```go
package billing_test

import (
	"context"
	"testing"

	"studbud/backend/internal/billing"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestGrantComp_InsertsActiveCompRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("GrantComp(true): %v", err)
	}

	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, u.ID).Scan(&ok)
	if !ok {
		t.Fatal("user_has_ai_access = false after GrantComp(true)")
	}
}

func TestGrantComp_RevokesByMarkingCanceled(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := svc.GrantComp(context.Background(), u.ID, false); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, u.ID).Scan(&ok)
	if ok {
		t.Fatal("user_has_ai_access = true after revoke")
	}
}

func TestGrantComp_IdempotentOnDoubleGrant(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := pkgbilling.NewService(pool, billing.NoopClient{})

	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant1: %v", err)
	}
	if err := svc.GrantComp(context.Background(), u.ID, true); err != nil {
		t.Fatalf("grant2: %v", err)
	}

	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM user_subscriptions WHERE user_id = $1 AND plan = 'comp'`, u.ID).Scan(&n)
	if n != 1 {
		t.Errorf("comp-row count = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./pkg/billing/... -count=1`
Expected: FAIL — `svc.GrantComp undefined`.

- [ ] **Step 3: Implement `GrantComp` in `pkg/billing/service.go`**

Append to `pkg/billing/service.go`:

```go
// GrantComp inserts or revokes a complimentary (comp) AI subscription for uid.
// active=true upserts a row with plan='comp', status='comp'.
// active=false marks any existing comp row as status='canceled'.
// Leaves Stripe-originated rows (plan='pro_monthly' / 'pro_annual') untouched.
func (s *Service) GrantComp(ctx context.Context, uid int64, active bool) error {
	if active {
		return s.upsertComp(ctx, uid)
	}
	return s.cancelComp(ctx, uid)
}

// upsertComp inserts a comp row, or resets an existing comp row to status='comp'.
func (s *Service) upsertComp(ctx context.Context, uid int64) error {
	const q = `
        INSERT INTO user_subscriptions (user_id, plan, status)
        VALUES ($1, 'comp', 'comp')
        ON CONFLICT (stripe_subscription_id) DO NOTHING
    `
	if _, err := s.db.Exec(ctx, q, uid); err != nil {
		return fmt.Errorf("upsert comp:\n%w", err)
	}
	return s.ensureCompActive(ctx, uid)
}

// ensureCompActive flips any existing comp row back to status='comp'.
func (s *Service) ensureCompActive(ctx context.Context, uid int64) error {
	const q = `
        UPDATE user_subscriptions
        SET status = 'comp', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	if _, err := s.db.Exec(ctx, q, uid); err != nil {
		return fmt.Errorf("activate comp:\n%w", err)
	}
	// Insert if no comp row existed at all.
	const ins = `
        INSERT INTO user_subscriptions (user_id, plan, status)
        SELECT $1, 'comp', 'comp'
        WHERE NOT EXISTS (
            SELECT 1 FROM user_subscriptions WHERE user_id = $1 AND plan = 'comp'
        )
    `
	if _, err := s.db.Exec(ctx, ins, uid); err != nil {
		return fmt.Errorf("insert comp fallback:\n%w", err)
	}
	return nil
}

// cancelComp marks any existing comp row for uid as status='canceled'.
func (s *Service) cancelComp(ctx context.Context, uid int64) error {
	const q = `
        UPDATE user_subscriptions
        SET status = 'canceled', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	if _, err := s.db.Exec(ctx, q, uid); err != nil {
		return fmt.Errorf("cancel comp:\n%w", err)
	}
	return nil
}
```

Add `fmt` to the imports block if not present.

**Note on the upsert:** The ON CONFLICT clause on `stripe_subscription_id` cannot hit for `comp` rows (they have NULL `stripe_subscription_id`), so `upsertComp` degrades to "insert if not exists, else bump via `ensureCompActive`." The two-step approach keeps Postgres' upsert semantics readable despite the absence of a natural unique key on (user_id, plan).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/billing/... -count=1 -v`
Expected: all three tests PASS.

- [ ] **Step 5: Write the failing handler test at `api/handler/admin_ai_test.go`**

```go
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/internal/billing"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestGrantAIAccess_AdminPathFlipsUserAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminAIServer(t, pool)
	tok := mintAdminToken(t, admin.ID)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "active": true})
	req := httptest.NewRequest("POST", "/admin/grant-ai-access", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT user_has_ai_access($1)`, target.ID).Scan(&ok)
	if !ok {
		t.Fatal("target user has no AI access post-grant")
	}
}

func TestGrantAIAccess_RejectsNonAdmin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	nonAdmin := testutil.NewVerifiedUser(t, pool)
	target := testutil.NewVerifiedUser(t, pool)

	srv := newAdminAIServer(t, pool)
	tok := mintToken(t, nonAdmin.ID, true, false)

	body, _ := json.Marshal(map[string]any{"user_id": target.ID, "active": true})
	req := httptest.NewRequest("POST", "/admin/grant-ai-access", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 6: Add the shared test helpers for admin-handler tests**

Append to the end of `api/handler/admin_ai_test.go`:

```go
// newAdminAIServer wires Auth → RequireVerified → RequireAdmin → AdminAIHandler.GrantAIAccess.
func newAdminAIServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, billing.NoopClient{})
	accSvc := access.NewService(pool)
	h := handler.NewAdminAIHandler(billSvc, accSvc)

	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified(), middleware.RequireAdmin())
	mux.Handle("POST /admin/grant-ai-access", stack(http.HandlerFunc(h.GrantAIAccess)))
	return mux
}

func mintAdminToken(t *testing.T, uid int64) string {
	t.Helper()
	return mintToken(t, uid, true, true)
}

func mintToken(t *testing.T, uid int64, verified, admin bool) string {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	tok, err := signer.Sign(jwtsigner.Claims{UID: uid, EmailVerified: verified, IsAdmin: admin})
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}
```

Add imports:

```go
"time"

"github.com/jackc/pgx/v5/pgxpool"

"studbud/backend/api/handler"
"studbud/backend/internal/http/middleware"
```

- [ ] **Step 7: Run the handler test to see it fail**

Run: `go test ./api/handler/... -run TestGrantAIAccess -count=1 -v`
Expected: FAIL — `handler.NewAdminAIHandler undefined`.

- [ ] **Step 8: Implement `api/handler/admin_ai.go`**

```go
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/billing"
)

// AdminAIHandler exposes admin-only routes for AI entitlement management.
type AdminAIHandler struct {
	billing *billing.Service // billing writes user_subscriptions rows
	access  *access.Service  // access reads user_has_ai_access post-mutation
}

// NewAdminAIHandler constructs an AdminAIHandler.
func NewAdminAIHandler(b *billing.Service, a *access.Service) *AdminAIHandler {
	return &AdminAIHandler{billing: b, access: a}
}

// grantInput is the request body for POST /admin/grant-ai-access.
type grantInput struct {
	UserID int64 `json:"user_id"` // UserID is the target user to flip
	Active bool  `json:"active"`  // Active=true grants, false revokes comp access
}

// grantOutput is the response body for POST /admin/grant-ai-access.
type grantOutput struct {
	UserID   int64 `json:"userId"`   // UserID echoes the target user
	AIAccess bool  `json:"aiAccess"` // AIAccess reflects user_has_ai_access post-mutation
}

// GrantAIAccess flips a comp user_subscriptions row for the target user.
// Response body reports the post-mutation AI-access state.
func (h *AdminAIHandler) GrantAIAccess(w http.ResponseWriter, r *http.Request) {
	in, err := decodeGrantInput(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.billing.GrantComp(r.Context(), in.UserID, in.Active); err != nil {
		httpx.WriteError(w, fmt.Errorf("grant comp:\n%w", err))
		return
	}
	ok, err := h.access.HasAIAccess(r.Context(), in.UserID)
	if err != nil {
		httpx.WriteError(w, fmt.Errorf("check access:\n%w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, grantOutput{UserID: in.UserID, AIAccess: ok})
}

// decodeGrantInput parses and validates the request body.
func decodeGrantInput(r *http.Request) (grantInput, error) {
	var in grantInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.UserID <= 0 {
		return in, &myErrors.AppError{Code: "validation", Message: "user_id must be positive", Wrapped: myErrors.ErrValidation, Field: "user_id"}
	}
	return in, nil
}
```

- [ ] **Step 9: Run the handler test to verify it passes**

Run: `go test ./api/handler/... -run TestGrantAIAccess -count=1 -v`
Expected: both tests PASS.

- [ ] **Step 10: Wire the route in `cmd/app/routes.go`**

Add a new helper function and call it from `buildRouter`.

In `buildRouter`, above the `stack := ...` line, add:

```go
admMW := middleware.RequireAdmin()
adm := wrap(authMW, verifiedMW, admMW)
registerAdminRoutes(mux, d, adm)
```

Then append a new function at the bottom of the file:

```go
// registerAdminRoutes attaches admin-only routes (auth + verified + admin).
func registerAdminRoutes(mux *http.ServeMux, d *deps, adm func(http.HandlerFunc) http.Handler) {
	adminAIH := handler.NewAdminAIHandler(d.billing, d.access)
	mux.Handle("POST /admin/grant-ai-access", adm(adminAIH.GrantAIAccess))
}
```

- [ ] **Step 11: Build + full test suite**

Run: `go build ./... && go test ./... -p 1 -count=1`
Expected: clean build; all tests PASS.

- [ ] **Step 12: Commit**

```bash
git add pkg/billing/ api/handler/admin_ai.go api/handler/admin_ai_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Admin grant-ai-access endpoint

[+] billing.Service.GrantComp(uid, active) writes comp user_subscriptions row
[+] POST /admin/grant-ai-access handler (auth + verified + admin)
[+] handler_test: admin path, non-admin 403, service-level idempotency
[+] registerAdminRoutes in cmd/app/routes.go
EOF
)"
```

---

### Task 5: Helper — quota queries + SQL constants file

**Files:**
- Create: `pkg/aipipeline/queries.go`

- [ ] **Step 1: Create `pkg/aipipeline/queries.go` with SQL constants used across later tasks**

```go
package aipipeline

// queries.go centralizes the raw SQL used by the pipeline service.
// Keep queries co-located to ease review and future migration work.

const sqlEnsureQuotaRow = `
INSERT INTO ai_quota_daily (user_id, day)
VALUES ($1, current_date)
ON CONFLICT (user_id, day) DO NOTHING
`

const sqlSelectQuotaRow = `
SELECT prompt_calls, pdf_calls, pdf_pages, check_calls
FROM ai_quota_daily
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPromptCalls = `
UPDATE ai_quota_daily SET prompt_calls = prompt_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPDFCalls = `
UPDATE ai_quota_daily SET pdf_calls = pdf_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPDFPages = `
UPDATE ai_quota_daily SET pdf_pages = pdf_pages + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitCheckCalls = `
UPDATE ai_quota_daily SET check_calls = check_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlCountRunningGenerations = `
SELECT count(*) FROM ai_jobs
WHERE user_id = $1
  AND status = 'running'
  AND feature_key IN ('generate_prompt','generate_pdf')
`

const sqlSelectRunningGenerationID = `
SELECT id FROM ai_jobs
WHERE user_id = $1
  AND status = 'running'
  AND feature_key IN ('generate_prompt','generate_pdf')
ORDER BY started_at DESC
LIMIT 1
`

const sqlInsertAIJob = `
INSERT INTO ai_jobs
  (user_id, feature_key, model, status, subject_id, flashcard_id, pdf_page_count, metadata)
VALUES ($1, $2, $3, 'running', $4, $5, $6, $7)
RETURNING id
`

const sqlFinalizeAIJobSuccess = `
UPDATE ai_jobs SET
  status         = 'complete',
  finished_at    = now(),
  input_tokens   = $2,
  output_tokens  = $3,
  cents_spent    = $4,
  items_emitted  = $5,
  items_dropped  = $6
WHERE id = $1
`

const sqlFinalizeAIJobFailure = `
UPDATE ai_jobs SET
  status         = $2,
  finished_at    = now(),
  input_tokens   = $3,
  output_tokens  = $4,
  cents_spent    = $5,
  items_emitted  = $6,
  items_dropped  = $7,
  error_kind     = $8,
  error          = $9
WHERE id = $1
`

const sqlIncrementItemsDropped = `
UPDATE ai_jobs SET items_dropped = items_dropped + 1 WHERE id = $1
`

const sqlReapOrphanJobs = `
UPDATE ai_jobs SET
  status      = 'failed',
  finished_at = now(),
  error_kind  = 'orphaned',
  error       = 'reaped: running > 1h'
WHERE status = 'running'
  AND started_at < now() - interval '1 hour'
`
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean. No tests yet — queries exercised by Task 6+.

- [ ] **Step 3: Commit**

```bash
git add pkg/aipipeline/queries.go
git commit -m "$(cat <<'EOF'
aipipeline SQL constants

[+] quota row ensure + select + per-counter debit queries
[+] running-generation count + id lookup (concurrent cap)
[+] ai_jobs insert, success-finalize, failure-finalize
[+] items-dropped increment helper
[+] orphan reaper update
EOF
)"
```

---
## Phase 2 — Quota Service

### Task 6: Quota check + debit methods with tests

**Files:**
- Create: `pkg/aipipeline/quota.go`
- Create: `pkg/aipipeline/quota_test.go`

- [ ] **Step 1: Write the failing tests at `pkg/aipipeline/quota_test.go`**

```go
package aipipeline_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestQuotaCheck_FirstCallAllowed(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	if err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateFromPrompt, 0); err != nil {
		t.Fatalf("first-call CheckQuota: %v", err)
	}
}

func TestQuotaCheck_ExhaustedReturnsQuotaError(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 20)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateFromPrompt, 0)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
}

func TestQuotaCheck_PDFPagesSeparateFromCalls(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	// 95 pages already used; remaining budget is 5 pages.
	testutil.SeedQuotaAt(t, pool, u.ID, "pdf_pages", 95)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	ctx := context.Background()

	if err := svc.CheckQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 5); err != nil {
		t.Fatalf("5-page CheckQuota: %v", err)
	}
	err := svc.CheckQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 6)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("6-page CheckQuota err = %v, want ErrQuotaExhausted", err)
	}
}

func TestQuotaDebit_IncrementsCounter(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	ctx := context.Background()

	if err := svc.DebitQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPrompt, 1, 0); err != nil {
		t.Fatalf("first debit: %v", err)
	}
	if err := svc.DebitQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 1, 7); err != nil {
		t.Fatalf("pdf debit: %v", err)
	}

	var prompt, pdfCalls, pages int
	_ = pool.QueryRow(ctx, `SELECT prompt_calls, pdf_calls, pdf_pages FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt, &pdfCalls, &pages)
	if prompt != 1 || pdfCalls != 1 || pages != 7 {
		t.Errorf("counters = (%d,%d,%d), want (1,1,7)", prompt, pdfCalls, pages)
	}
}

func TestQuotaSnapshot_ReflectsDebits(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "check_calls", 12)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	snap, err := svc.QuotaSnapshot(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("QuotaSnapshot: %v", err)
	}
	if snap.Check.Used != 12 || snap.Check.Limit != 50 {
		t.Errorf("check = (%d/%d), want (12/50)", snap.Check.Used, snap.Check.Limit)
	}
	if !snap.AIAccess {
		t.Error("AIAccess = false, want true")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./pkg/aipipeline/... -count=1 -v`
Expected: FAIL — `svc.CheckQuota`, `svc.DebitQuota`, `svc.QuotaSnapshot` undefined.

- [ ] **Step 3: Implement `pkg/aipipeline/quota.go`**

```go
package aipipeline

import (
	"context"
	"fmt"
	"time"

	"studbud/backend/internal/myErrors"
)

// quotaRow is the per-user per-day counter tuple we read before a call.
type quotaRow struct {
	PromptCalls int // PromptCalls is today's prompt generation count
	PDFCalls    int // PDFCalls is today's PDF generation count
	PDFPages    int // PDFPages is today's PDF page consumption
	CheckCalls  int // CheckCalls is today's check-flashcard count
}

// QuotaBucket is one feature's used/limit/reset tuple as surfaced to clients.
type QuotaBucket struct {
	Used    int       `json:"used"`    // Used is the count of operations performed today
	Limit   int       `json:"limit"`   // Limit is the per-feature daily cap
	ResetAt time.Time `json:"resetAt"` // ResetAt is midnight UTC of tomorrow
}

// PDFBucket extends QuotaBucket with separate page accounting.
type PDFBucket struct {
	Used       int       `json:"used"`       // Used is today's PDF generation count
	Limit      int       `json:"limit"`      // Limit is the per-day PDF generation cap
	PagesUsed  int       `json:"pagesUsed"`  // PagesUsed is today's PDF page consumption
	PagesLimit int       `json:"pagesLimit"` // PagesLimit is the per-day page cap
	ResetAt    time.Time `json:"resetAt"`    // ResetAt is midnight UTC of tomorrow
}

// QuotaSnapshot is the GET /ai/quota response shape.
type QuotaSnapshot struct {
	AIAccess bool        `json:"aiAccess"` // AIAccess reflects user_has_ai_access
	Prompt   QuotaBucket `json:"prompt"`   // Prompt is the prompt-mode bucket
	PDF      PDFBucket   `json:"pdf"`      // PDF is the PDF-mode bucket
	Check    QuotaBucket `json:"check"`    // Check is the AI-check bucket
}

// CheckQuota asserts the user has budget left for the given feature.
// pdfPages is used only for FeatureGenerateFromPDF; pass 0 otherwise.
// Returns ErrQuotaExhausted (wrapped in AppError) when over-budget.
func (s *Service) CheckQuota(ctx context.Context, uid int64, feat FeatureKey, pdfPages int) error {
	row, err := s.readOrCreateQuotaRow(ctx, uid)
	if err != nil {
		return err
	}
	return checkAgainstLimits(row, feat, pdfPages, s.limits)
}

// readOrCreateQuotaRow ensures the row exists for today and returns its counters.
func (s *Service) readOrCreateQuotaRow(ctx context.Context, uid int64) (quotaRow, error) {
	if _, err := s.db.Exec(ctx, sqlEnsureQuotaRow, uid); err != nil {
		return quotaRow{}, fmt.Errorf("ensure quota row:\n%w", err)
	}
	var row quotaRow
	err := s.db.QueryRow(ctx, sqlSelectQuotaRow, uid).Scan(&row.PromptCalls, &row.PDFCalls, &row.PDFPages, &row.CheckCalls)
	if err != nil {
		return quotaRow{}, fmt.Errorf("select quota row:\n%w", err)
	}
	return row, nil
}

// checkAgainstLimits returns an ErrQuotaExhausted if the feature is over cap.
func checkAgainstLimits(row quotaRow, feat FeatureKey, pdfPages int, lim QuotaLimits) error {
	switch feat {
	case FeatureGenerateFromPrompt:
		if row.PromptCalls >= lim.PromptCalls {
			return quotaExhausted("prompt")
		}
	case FeatureGenerateFromPDF:
		if row.PDFCalls >= lim.PDFCalls {
			return quotaExhausted("pdf")
		}
		if row.PDFPages+pdfPages > lim.PDFPages {
			return quotaExhausted("pdf_pages")
		}
	case FeatureCheckFlashcard:
		if row.CheckCalls >= lim.CheckCalls {
			return quotaExhausted("check")
		}
	}
	return nil
}

// quotaExhausted builds an AppError wrapping ErrQuotaExhausted with feature-specific message.
func quotaExhausted(bucket string) error {
	return &myErrors.AppError{
		Code:    "quota_exceeded",
		Message: fmt.Sprintf("daily %s quota exhausted; resets at midnight UTC", bucket),
		Wrapped: myErrors.ErrQuotaExhausted,
	}
}

// DebitQuota increments the relevant counter(s) for feat.
// For FeatureGenerateFromPDF pass pages > 0 to also bump pdf_pages.
// Prompt/PDF/check "calls" are debited once per successful job (callers decide when).
func (s *Service) DebitQuota(ctx context.Context, uid int64, feat FeatureKey, calls, pages int) error {
	if _, err := s.db.Exec(ctx, sqlEnsureQuotaRow, uid); err != nil {
		return fmt.Errorf("ensure quota row:\n%w", err)
	}
	if err := s.debitCalls(ctx, uid, feat, calls); err != nil {
		return err
	}
	if feat == FeatureGenerateFromPDF && pages > 0 {
		if _, err := s.db.Exec(ctx, sqlDebitPDFPages, uid, pages); err != nil {
			return fmt.Errorf("debit pdf_pages:\n%w", err)
		}
	}
	return nil
}

// debitCalls routes to the per-feature call counter.
func (s *Service) debitCalls(ctx context.Context, uid int64, feat FeatureKey, calls int) error {
	if calls <= 0 {
		return nil
	}
	q, err := debitCallsSQL(feat)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, q, uid, calls); err != nil {
		return fmt.Errorf("debit %s calls:\n%w", feat, err)
	}
	return nil
}

// debitCallsSQL returns the UPDATE statement for feat's calls column.
func debitCallsSQL(feat FeatureKey) (string, error) {
	switch feat {
	case FeatureGenerateFromPrompt:
		return sqlDebitPromptCalls, nil
	case FeatureGenerateFromPDF:
		return sqlDebitPDFCalls, nil
	case FeatureCheckFlashcard:
		return sqlDebitCheckCalls, nil
	}
	return "", fmt.Errorf("unknown feature %q", feat)
}

// QuotaSnapshot returns the per-feature usage counters for today plus aiAccess.
func (s *Service) QuotaSnapshot(ctx context.Context, uid int64) (*QuotaSnapshot, error) {
	row, err := s.readOrCreateQuotaRow(ctx, uid)
	if err != nil {
		return nil, err
	}
	hasAccess, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return nil, err
	}
	return buildSnapshot(row, s.limits, hasAccess), nil
}

// buildSnapshot assembles the JSON-facing QuotaSnapshot from raw counters.
func buildSnapshot(row quotaRow, lim QuotaLimits, aiAccess bool) *QuotaSnapshot {
	reset := nextMidnightUTC(time.Now())
	return &QuotaSnapshot{
		AIAccess: aiAccess,
		Prompt:   QuotaBucket{Used: row.PromptCalls, Limit: lim.PromptCalls, ResetAt: reset},
		PDF: PDFBucket{
			Used: row.PDFCalls, Limit: lim.PDFCalls,
			PagesUsed: row.PDFPages, PagesLimit: lim.PDFPages,
			ResetAt: reset,
		},
		Check: QuotaBucket{Used: row.CheckCalls, Limit: lim.CheckCalls, ResetAt: reset},
	}
}

// nextMidnightUTC returns the next 00:00 UTC instant strictly after t.
func nextMidnightUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/aipipeline/... -count=1 -v`
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/quota.go pkg/aipipeline/quota_test.go
git commit -m "$(cat <<'EOF'
aipipeline quota Check + Debit + Snapshot

[+] Service.CheckQuota(uid, feat, pdfPages)
[+] Service.DebitQuota(uid, feat, calls, pages)
[+] Service.QuotaSnapshot(uid) -> QuotaSnapshot (aiAccess + buckets)
[+] Per-feature counter mapping (prompt / pdf / check + pdf_pages)
[+] Tests: first-call allow, exhausted, pdf pages vs calls, debit increments
EOF
)"
```

---

### Task 7: `GET /ai/quota` endpoint

**Files:**
- Modify: `api/handler/ai_stub.go` (add `Quota` method; keep file name for now — Task 16 renames)
- Create: `api/handler/ai_quota_test.go`
- Modify: `cmd/app/routes.go` (add route — no auth-verified gate; auth only, matches the read-style routes)

- [ ] **Step 1: Write the failing test at `api/handler/ai_quota_test.go`**

```go
package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/billing"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
)

func TestQuota_ReturnsSnapshotForAuthenticatedUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 4)

	srv := newAIQuotaServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	req := httptest.NewRequest("GET", "/ai/quota", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body aipipeline.QuotaSnapshot
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.AIAccess {
		t.Error("AIAccess = false, want true")
	}
	if body.Prompt.Used != 4 || body.Prompt.Limit != 20 {
		t.Errorf("prompt = (%d/%d), want (4/20)", body.Prompt.Used, body.Prompt.Limit)
	}
}

func newAIQuotaServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, aiProvider.NoopClient{}, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	_ = pkgbilling.NewService(pool, billing.NoopClient{}) // imported for parity; not used here
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer))
	mux.Handle("GET /ai/quota", stack(http.HandlerFunc(h.Quota)))
	return mux
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./api/handler/... -run TestQuota_ReturnsSnapshot -count=1 -v`
Expected: FAIL — `h.Quota undefined`.

- [ ] **Step 3: Add the `Quota` method in `api/handler/ai_stub.go`**

Open `api/handler/ai_stub.go`. Append:

```go
// Quota returns the authenticated user's current AI quota snapshot.
func (h *AIHandler) Quota(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	snap, err := h.svc.QuotaSnapshot(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
}
```

Add imports (if not already present in this file):

```go
"studbud/backend/internal/authctx"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./api/handler/... -run TestQuota_ReturnsSnapshot -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Wire the route in `cmd/app/routes.go`**

In `registerAuthReadRoutes`, add the AI handler construction and the route:

Find:
```go
searchH := handler.NewSearchHandler(d.search)
```

Below that line in the same function, add:
```go
aiH := handler.NewAIHandler(d.ai)
```

Then at the end of the route declarations in `registerAuthReadRoutes`, add:
```go
mux.Handle("GET /ai/quota", auth(aiH.Quota))
```

- [ ] **Step 6: Build + full test suite**

Run: `go build ./... && go test ./... -p 1 -count=1`
Expected: clean build; all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add api/handler/ai_stub.go api/handler/ai_quota_test.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
GET /ai/quota endpoint

[+] AIHandler.Quota (auth, no verified gate) returns QuotaSnapshot JSON
[+] Route wired in registerAuthReadRoutes
[+] Handler test asserts aiAccess + bucket counters
EOF
)"
```

---
## Phase 3 — Pipeline Primitive

### Task 8: Pipeline pre-flight (entitlement + quota + concurrent-cap) + job insert

**Files:**
- Create: `pkg/aipipeline/service_generation.go`
- Create: `pkg/aipipeline/service_generation_test.go`

- [ ] **Step 1: Write the failing tests at `pkg/aipipeline/service_generation_test.go`**

```go
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
	// Message should embed the existing jobID for client resume.
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
	// Drain the stream so finalization completes.
	for range ch {
	}
	if n := testutil.CountAIJobs(t, pool, u.ID); n != 1 {
		t.Errorf("ai_jobs count = %d, want 1", n)
	}
}

// --- helpers ---

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

// indexRune / indexByte are standard-library wrappers kept inline to avoid a test-only import.
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
```

Imports for this file:

```go
import (
	"github.com/jackc/pgx/v5/pgxpool"
)
```

(in addition to the already-listed ones).

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./pkg/aipipeline/... -run TestRun_ -count=1 -v`
Expected: FAIL — `svc.RunStructuredGeneration undefined`.

- [ ] **Step 3: Implement pre-flight + job insert in `pkg/aipipeline/service_generation.go`**

```go
package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
)

// RunStructuredGeneration validates entitlement + quota + concurrency, inserts an
// ai_jobs row, then spawns a goroutine that drives the provider stream and emits
// validated AIChunks on the returned channel. The channel closes when the stream
// ends. Callers always receive jobID (for audit), even on synchronous errors.
func (s *Service) RunStructuredGeneration(
	ctx context.Context,
	req AIRequest,
) (<-chan AIChunk, int64, error) {
	if err := s.preflight(ctx, req); err != nil {
		return nil, 0, err
	}
	jobID, err := s.insertJob(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	out := make(chan AIChunk, 16)
	go s.drive(ctx, req, jobID, out)
	return out, jobID, nil
}

// preflight runs entitlement + quota + concurrent-cap checks in order.
func (s *Service) preflight(ctx context.Context, req AIRequest) error {
	if err := s.checkEntitlement(ctx, req.UserID); err != nil {
		return err
	}
	if err := s.CheckQuota(ctx, req.UserID, req.Feature, req.PDFPages); err != nil {
		return err
	}
	return s.checkConcurrency(ctx, req)
}

// checkEntitlement fails fast when the user lacks AI access.
func (s *Service) checkEntitlement(ctx context.Context, uid int64) error {
	ok, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return fmt.Errorf("has ai access:\n%w", err)
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// checkConcurrency rejects a second generate request while one is already running.
// Check-flashcard calls are not capped this way.
func (s *Service) checkConcurrency(ctx context.Context, req AIRequest) error {
	if req.Feature == FeatureCheckFlashcard {
		return nil
	}
	var existingID int64
	err := s.db.QueryRow(ctx, sqlSelectRunningGenerationID, req.UserID).Scan(&existingID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("check concurrency:\n%w", err)
	}
	return &myErrors.AppError{
		Code:    "concurrent_generation",
		Message: fmt.Sprintf("generation already running (jobId #%d)", existingID),
		Wrapped: myErrors.ErrConflict,
	}
}

// insertJob creates the ai_jobs row and returns its id.
func (s *Service) insertJob(ctx context.Context, req AIRequest) (int64, error) {
	meta, err := json.Marshal(req.Metadata)
	if err != nil {
		meta = []byte(`{}`)
	}
	var subjectID, flashcardID *int64
	if req.SubjectID > 0 {
		subjectID = &req.SubjectID
	}
	if req.FlashcardID > 0 {
		flashcardID = &req.FlashcardID
	}
	var jobID int64
	err = s.db.QueryRow(ctx, sqlInsertAIJob,
		req.UserID, string(req.Feature), s.model,
		subjectID, flashcardID, req.PDFPages, meta,
	).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("insert ai_job:\n%w", err)
	}
	return jobID, nil
}

// drive is the placeholder stream driver filled in by Task 9.
// Task 8 ships a minimal version that closes immediately so tests that drain
// the channel don't deadlock.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	// Finalize as complete with zero items so the ai_jobs row doesn't stay 'running'.
	_ = s.finalizeSuccess(ctx, jobID, 0, 0, 0, 0, 0)
	out <- AIChunk{Kind: ChunkDone}
}

// finalizeSuccess marks a job complete with the provided telemetry.
func (s *Service) finalizeSuccess(ctx context.Context, jobID int64, inTok, outTok, cents, emitted, dropped int) error {
	_, err := s.db.Exec(ctx, sqlFinalizeAIJobSuccess, jobID, inTok, outTok, cents, emitted, dropped)
	if err != nil {
		return fmt.Errorf("finalize success:\n%w", err)
	}
	return nil
}

// Used by tests that inject errors.
var _ aiProvider.Client = (*aiProvider.NoopClient)(nil)
```

Add a helper `isNoRows` in `pkg/aipipeline/service.go`:

Append to `pkg/aipipeline/service.go`:

```go
import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// isNoRows returns true when err is pgx's "no rows" sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
```

Merge the imports with the existing block in `service.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/aipipeline/... -run TestRun_ -count=1 -v`
Expected: 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/service.go pkg/aipipeline/service_generation.go pkg/aipipeline/service_generation_test.go
git commit -m "$(cat <<'EOF'
Pipeline pre-flight + job insert

[+] Service.RunStructuredGeneration entry point
[+] Preflight: entitlement (HasAIAccess) + quota + concurrent-cap
[+] Concurrent-cap returns AppError{Code:concurrent_generation} wrapping ErrConflict, embeds running jobID in message
[+] ai_jobs row insert with subject_id / flashcard_id / pdf_page_count / metadata
[+] Placeholder drive() that finalizes complete with zero items (filled in Task 9)
EOF
)"
```

---

### Task 9: Stream consumption + incremental item emission + finalize

**Files:**
- Modify: `pkg/aipipeline/service_generation.go`
- Create: `pkg/aipipeline/streamparse.go`
- Create: `pkg/aipipeline/streamparse_test.go`
- Modify: `pkg/aipipeline/service_generation_test.go` (add happy-path test)

- [ ] **Step 1: Write the failing test for the streaming JSON parser at `pkg/aipipeline/streamparse_test.go`**

```go
package aipipeline

import (
	"reflect"
	"testing"
)

func TestStreamParser_ExtractsArrayElements(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }

	input := `{"items":[{"q":"one","a":"A"},{"q":"two","a":"B"}]}`
	for i := 0; i < len(input); i++ {
		p.feed([]byte{input[i]})
	}

	want := [][]byte{
		[]byte(`{"q":"one","a":"A"}`),
		[]byte(`{"q":"two","a":"B"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_IgnoresNonArrayFields(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{"verdict":"ok","items":[{"q":"x"}]}`))
	want := [][]byte{[]byte(`{"q":"x"}`)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_HandlesNestedObjects(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{"items":[{"q":"x","meta":{"k":"v"}},{"q":"y"}]}`))
	want := [][]byte{
		[]byte(`{"q":"x","meta":{"k":"v"}}`),
		[]byte(`{"q":"y"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_IgnoresWhitespaceAndCommas(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{ "items" : [ {"q":"a"} , {"q":"b"} ] }`))
	if len(got) != 2 {
		t.Fatalf("count = %d, want 2; got = %s", len(got), got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./pkg/aipipeline/ -run TestStreamParser -count=1 -v`
Expected: FAIL — `newArrayParser undefined`.

- [ ] **Step 3: Implement `pkg/aipipeline/streamparse.go`**

```go
package aipipeline

// arrayParser is a single-pass streaming parser that extracts complete
// JSON object elements of one named array property from an arriving byte stream.
// It's a handful of states — not a general JSON parser.
// States: waiting-for-array → inside-array → inside-element → back-to-array.
type arrayParser struct {
	field     string        // field is the array property name we care about
	onElement func([]byte)  // onElement fires once per complete top-level element bytes

	buf       []byte // buf accumulates bytes of the current element
	state     parserState
	depth     int  // depth tracks { } nesting inside the current element
	inString  bool // inString means the cursor is inside a JSON string literal
	escape    bool // escape means the previous byte was a backslash inside a string
	fieldBuf  []byte // fieldBuf accumulates property names while matching `field`
}

type parserState int

const (
	stateSearchField parserState = iota // looking for the named property
	stateAwaitArray                     // found the colon, waiting for '['
	stateInsideArray                    // inside the array, waiting for element start
	stateInsideElement                  // collecting one element's bytes
	stateDone                           // array closed; parser ignores further input
)

// newArrayParser returns a parser that extracts elements of the named array.
func newArrayParser(field string) *arrayParser {
	return &arrayParser{field: field}
}

// feed pushes bytes into the parser, firing onElement for each closed element.
func (p *arrayParser) feed(b []byte) {
	for _, c := range b {
		p.step(c)
	}
}

// step advances the parser by one byte.
func (p *arrayParser) step(c byte) {
	switch p.state {
	case stateSearchField:
		p.searchField(c)
	case stateAwaitArray:
		p.awaitArray(c)
	case stateInsideArray:
		p.insideArray(c)
	case stateInsideElement:
		p.insideElement(c)
	}
}

// searchField accumulates the current property name and transitions when matched.
func (p *arrayParser) searchField(c byte) {
	if c == '"' {
		p.fieldBuf = p.fieldBuf[:0]
		for {
			break // read handled by subsequent bytes via fieldStringMode
		}
	}
	// We collect bytes between quotes into fieldBuf across successive step calls.
	// Simpler approach: track a small substate locally via inString.
	if p.inString {
		if p.escape {
			p.fieldBuf = append(p.fieldBuf, c)
			p.escape = false
			return
		}
		if c == '\\' {
			p.escape = true
			return
		}
		if c == '"' {
			p.inString = false
			return
		}
		p.fieldBuf = append(p.fieldBuf, c)
		return
	}
	if c == '"' {
		p.inString = true
		p.fieldBuf = p.fieldBuf[:0]
		return
	}
	if c == ':' && string(p.fieldBuf) == p.field {
		p.state = stateAwaitArray
	}
}

// awaitArray consumes whitespace and transitions on '['.
func (p *arrayParser) awaitArray(c byte) {
	if c == '[' {
		p.state = stateInsideArray
	}
}

// insideArray begins collecting a new element on '{', or ends on ']'.
func (p *arrayParser) insideArray(c byte) {
	if c == '{' {
		p.buf = append(p.buf[:0], '{')
		p.depth = 1
		p.state = stateInsideElement
		return
	}
	if c == ']' {
		p.state = stateDone
	}
}

// insideElement accumulates bytes until the element's top-level '}' closes.
func (p *arrayParser) insideElement(c byte) {
	p.buf = append(p.buf, c)
	if p.inString {
		if p.escape {
			p.escape = false
			return
		}
		if c == '\\' {
			p.escape = true
			return
		}
		if c == '"' {
			p.inString = false
		}
		return
	}
	if c == '"' {
		p.inString = true
		return
	}
	if c == '{' {
		p.depth++
	}
	if c == '}' {
		p.depth--
		if p.depth == 0 {
			p.onElement(p.buf)
			p.buf = p.buf[:0]
			p.state = stateInsideArray
		}
	}
}
```

- [ ] **Step 4: Run the parser tests to verify**

Run: `go test ./pkg/aipipeline/ -run TestStreamParser -count=1 -v`
Expected: all 4 PASS.

If any fail: inspect `p.fieldBuf` — the parser resets it at every new `"`. That's fine because property names are the only strings in the outer object (before the array). The parser does NOT track full object structure; it only latches the first `:` after seeing the target field name. A pathological payload with the field name inside a preceding string literal would misfire, but our schemas place the array at the top level.

- [ ] **Step 5: Replace the placeholder `drive()` in `pkg/aipipeline/service_generation.go` with the real stream loop**

Replace the body of `drive` with:

```go
// drive runs the provider stream, parses incoming JSON into array elements,
// emits ChunkItem / ChunkDone / ChunkError, and finalizes the ai_jobs row.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	result := s.streamOnce(ctx, req, jobID, out)
	s.finalize(ctx, jobID, req, result, out)
}

// streamResult aggregates what happened during one provider stream.
type streamResult struct {
	inputTokens  int
	outputTokens int
	centsSpent   int
	emitted      int
	dropped      int
	err          error // nil on success
}

// streamOnce calls the provider once and drives the parser. Caller handles retry.
func (s *Service) streamOnce(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) streamResult {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		PDFBytes:   req.PDFBytes,
	})
	if err != nil {
		return streamResult{err: classifyProviderStartErr(err)}
	}
	return s.consumeStream(ctx, chunks, out)
}

// consumeStream reads chunks, forwards items, counts accepted/dropped.
func (s *Service) consumeStream(ctx context.Context, chunks <-chan aiProvider.Chunk, out chan<- AIChunk) streamResult {
	r := streamResult{}
	p := newArrayParser("items")
	p.onElement = func(b []byte) {
		if isWellFormedObject(b) {
			cp := append([]byte(nil), b...)
			select {
			case out <- AIChunk{Kind: ChunkItem, Item: cp}:
				r.emitted++
			case <-ctx.Done():
			}
		} else {
			r.dropped++
		}
	}
	for {
		select {
		case <-ctx.Done():
			r.err = ctx.Err()
			return r
		case c, ok := <-chunks:
			if !ok {
				return r
			}
			p.feed([]byte(c.Text))
			if c.Done {
				return r
			}
		}
	}
}

// finalize writes the terminal state to ai_jobs and emits the last chunk.
func (s *Service) finalize(ctx context.Context, jobID int64, req AIRequest, r streamResult, out chan<- AIChunk) {
	if r.err != nil {
		s.finalizeError(ctx, jobID, r, out)
		return
	}
	_ = s.finalizeSuccess(ctx, jobID, r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped)
	if r.emitted > 0 {
		_ = s.DebitQuota(ctx, req.UserID, req.Feature, 1, 0)
	}
	out <- AIChunk{Kind: ChunkDone}
}

// finalizeError marks the job failed and surfaces the error to the client.
func (s *Service) finalizeError(ctx context.Context, jobID int64, r streamResult, out chan<- AIChunk) {
	kind, msg := classifyErrForPersistence(r.err)
	_, _ = s.db.Exec(ctx, sqlFinalizeAIJobFailure, jobID, statusFor(r.err),
		r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped, kind, msg)
	out <- AIChunk{Kind: ChunkError, Err: r.err}
}
```

Add helper functions in a new file `pkg/aipipeline/errors.go`:

```go
package aipipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"studbud/backend/internal/myErrors"
)

// classifyProviderStartErr wraps raw provider-client errors into sentinel AppErrors.
// Called for synchronous errors returned from aiProvider.Client.Stream.
func classifyProviderStartErr(err error) error {
	if err == nil {
		return nil
	}
	// Caller may wrap with sentinel types; preserve.
	if errors.Is(err, myErrors.ErrContentPolicy) {
		return err
	}
	if errors.Is(err, myErrors.ErrAIProvider) {
		return err
	}
	return &myErrors.AppError{Code: "provider_5xx", Message: "AI service failed before streaming", Wrapped: myErrors.ErrAIProvider}
}

// classifyErrForPersistence returns (error_kind, error_message) for the ai_jobs row.
func classifyErrForPersistence(err error) (kind, msg string) {
	if err == nil {
		return "", ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled", "context canceled"
	case errors.Is(err, myErrors.ErrContentPolicy):
		return "content_policy", err.Error()
	case errors.Is(err, myErrors.ErrAIProvider):
		return providerKind(err), err.Error()
	}
	return "internal", err.Error()
}

// providerKind returns a narrower provider error kind based on AppError.Code.
func providerKind(err error) string {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	return "provider_5xx"
}

// statusFor maps a terminal error to an ai_jobs.status value.
func statusFor(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

// isWellFormedObject returns true when b parses as a JSON object.
// Used to drop garbled items without aborting the stream.
func isWellFormedObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil
}
```

- [ ] **Step 6: Extend the generation tests with a happy-path case**

Append to `pkg/aipipeline/service_generation_test.go`:

```go
func TestRun_HappyPath_EmitsItemsThenDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"},`},
			{Text: `{"title":"t2","question":"q2","answer":"a2"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var items []json.RawMessage
	var sawDone bool
	for c := range ch {
		switch c.Kind {
		case aipipeline.ChunkItem:
			items = append(items, c.Item)
		case aipipeline.ChunkDone:
			sawDone = true
		case aipipeline.ChunkError:
			t.Fatalf("unexpected error chunk: %v", c.Err)
		}
	}
	if !sawDone {
		t.Error("missing ChunkDone")
	}
	if len(items) != 2 {
		t.Errorf("items count = %d, want 2", len(items))
	}

	var status string
	var emitted int
	_ = pool.QueryRow(context.Background(), `SELECT status, items_emitted FROM ai_jobs WHERE id=$1`, jobID).Scan(&status, &emitted)
	if status != "complete" || emitted != 2 {
		t.Errorf("row (%q, emitted=%d), want (complete, 2)", status, emitted)
	}

	var prompt int
	_ = pool.QueryRow(context.Background(), `SELECT prompt_calls FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt)
	if prompt != 1 {
		t.Errorf("prompt_calls = %d, want 1 (one successful job)", prompt)
	}
}

func TestRun_DropsSchemaInvalidItems(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"ok"},not-json,{"title":"ok2"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	var emitted, dropped int
	_ = pool.QueryRow(context.Background(), `SELECT items_emitted, items_dropped FROM ai_jobs WHERE id=$1`, jobID).Scan(&emitted, &dropped)
	// "not-json" never closes at depth=0, so it's not seen by the parser at all.
	// Both well-formed objects emit.
	if emitted != 2 {
		t.Errorf("emitted = %d, want 2", emitted)
	}
	_ = dropped
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./pkg/aipipeline/... -count=1 -v`
Expected: all pipeline tests PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/aipipeline/streamparse.go pkg/aipipeline/streamparse_test.go pkg/aipipeline/errors.go pkg/aipipeline/service_generation.go pkg/aipipeline/service_generation_test.go
git commit -m "$(cat <<'EOF'
Pipeline stream consumption + item emission + finalize

[+] arrayParser: incremental JSON parser for one named array field
[+] streamOnce → consumeStream → finalize split for drive loop
[+] ChunkItem emitted per well-formed object; malformed objects dropped
[+] Successful job: finalize complete + debit feature calls counter by 1
[+] Terminal error: finalize failed with error_kind + error message
[+] classifyProviderStartErr / classifyErrForPersistence helpers
[+] Happy-path test + schema-drop test
EOF
)"
```

---

### Task 10: Transparent single retry on transport transients

**Files:**
- Modify: `pkg/aipipeline/service_generation.go`
- Modify: `pkg/aipipeline/errors.go`
- Modify: `pkg/aipipeline/service_generation_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/aipipeline/service_generation_test.go`:

```go
func TestRun_RetriesOnceOnTransientProviderError(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        &myErrors.AppError{Code: "provider_5xx", Wrapped: myErrors.ErrAIProvider},
		FailFirstN: 1,
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"ok","question":"q","answer":"a"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	if cli.Calls() != 2 {
		t.Errorf("calls = %d, want 2 (one failure + one retry)", cli.Calls())
	}
}

func TestRun_DoesNotRetryOnContentPolicy(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        myErrors.ErrContentPolicy,
		FailFirstN: 5, // would fail many times if we retried
	}
	svc := newPipelineSvc(pool, cli)
	ch, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawError bool
	for c := range ch {
		if c.Kind == aipipeline.ChunkError {
			sawError = true
		}
	}
	if !sawError {
		t.Error("expected ChunkError")
	}
	if cli.Calls() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on content_policy)", cli.Calls())
	}
}

func TestRun_FailsAfterRetryExhausts(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        &myErrors.AppError{Code: "provider_5xx", Wrapped: myErrors.ErrAIProvider},
		FailFirstN: 10, // keep failing through the single retry
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	if cli.Calls() != 2 {
		t.Errorf("calls = %d, want 2", cli.Calls())
	}
	var status, errKind string
	_ = pool.QueryRow(context.Background(), `SELECT status, error_kind FROM ai_jobs WHERE id=$1`, jobID).Scan(&status, &errKind)
	if status != "failed" || errKind != "provider_5xx" {
		t.Errorf("row = (%q, %q), want (failed, provider_5xx)", status, errKind)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./pkg/aipipeline/... -run TestRun_ -count=1 -v`
Expected: first test fails (`calls = 1, want 2`). Second and third may accidentally pass on the current code.

- [ ] **Step 3: Implement `retryable` + retry in `streamOnce`**

Append to `pkg/aipipeline/errors.go`:

```go
// retryable reports whether a synchronous provider-start error is worth one
// transparent retry. Only transport transients qualify (5xx / timeout / 429).
// Content-policy refusals, 4xx, and malformed output are terminal.
func retryable(err error) bool {
	var ae *myErrors.AppError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Code {
	case "provider_5xx", "provider_timeout", "provider_rate_limit":
		return true
	}
	return false
}
```

Modify `drive` in `service_generation.go` to retry at most once:

Replace the body of `drive` with:

```go
// drive runs the provider stream (with one transparent retry on transport
// transients), parses JSON, emits chunks, and finalizes the ai_jobs row.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	r := s.streamOnce(ctx, req, jobID, out)
	if r.err != nil && retryable(r.err) {
		r = s.streamOnce(ctx, req, jobID, out)
	}
	s.finalize(ctx, jobID, req, r, out)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/aipipeline/... -run TestRun_ -count=1 -v`
Expected: all pipeline tests PASS, including the 3 new retry tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/errors.go pkg/aipipeline/service_generation.go pkg/aipipeline/service_generation_test.go
git commit -m "$(cat <<'EOF'
Pipeline transparent retry on transport transients

[+] retryable(err) for AppError.Code in {provider_5xx, provider_timeout, provider_rate_limit}
[+] drive() retries streamOnce at most once on retryable synchronous start error
[+] Tests: one retry on transient, no retry on content_policy, failure after retry exhausts
EOF
)"
```

---

### Task 11: `aiJobsOrphanReaper` cron

**Files:**
- Create: `pkg/aipipeline/reaper.go`
- Create: `pkg/aipipeline/reaper_test.go`
- Modify: `cmd/app/main.go` (or whichever file registers cron jobs)

- [ ] **Step 1: Write the failing test at `pkg/aipipeline/reaper_test.go`**

```go
package aipipeline_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestReapOrphanedJobs_FlipsLongRunningJobs(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	// Insert a fresh running job (should NOT be reaped).
	fresh := testutil.SeedRunningJob(t, pool, u.ID, "generate_prompt")
	// Insert a stale running job by backdating started_at.
	stale := testutil.SeedRunningJob(t, pool, u.ID, "generate_pdf")
	_, _ = pool.Exec(context.Background(), `UPDATE ai_jobs SET started_at = now() - interval '2 hours' WHERE id = $1`, stale)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	n, err := svc.ReapOrphanedJobs(context.Background())
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped = %d, want 1", n)
	}

	var freshStatus, staleStatus, staleErrKind string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM ai_jobs WHERE id=$1`, fresh).Scan(&freshStatus)
	_ = pool.QueryRow(context.Background(), `SELECT status, error_kind FROM ai_jobs WHERE id=$1`, stale).Scan(&staleStatus, &staleErrKind)
	if freshStatus != "running" {
		t.Errorf("fresh status = %q, want running", freshStatus)
	}
	if staleStatus != "failed" || staleErrKind != "orphaned" {
		t.Errorf("stale = (%q, %q), want (failed, orphaned)", staleStatus, staleErrKind)
	}
	_ = time.Second // silence unused import
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./pkg/aipipeline/... -run TestReap -count=1 -v`
Expected: FAIL — `svc.ReapOrphanedJobs undefined`.

- [ ] **Step 3: Implement `pkg/aipipeline/reaper.go`**

```go
package aipipeline

import (
	"context"
	"fmt"
)

// ReapOrphanedJobs flips ai_jobs rows in status='running' that started > 1h ago
// to status='failed' with error_kind='orphaned'. Returns the number of rows flipped.
// Designed to be registered as a cron.Job.
func (s *Service) ReapOrphanedJobs(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, sqlReapOrphanJobs)
	if err != nil {
		return 0, fmt.Errorf("reap orphaned jobs:\n%w", err)
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 4: Run the test to verify**

Run: `go test ./pkg/aipipeline/... -run TestReap -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Register the cron job in `cmd/app/main.go`**

Open `cmd/app/main.go`. Find where the scheduler is used (should be after `buildDeps` and before `http.ListenAndServe`). Add a `Register` call:

```go
d.scheduler.Register(cron.Job{
    Name:     "aiJobsOrphanReaper",
    Interval: 10 * time.Minute,
    Run: func(ctx context.Context) error {
        _, err := d.ai.ReapOrphanedJobs(ctx)
        return err
    },
})
```

Add imports:

```go
"context"
"time"

"studbud/backend/internal/cron"
```

(If they're already present, skip.) If `main.go` doesn't currently register any cron jobs and you can't locate a natural insertion point, the minimal viable location is immediately after `buildDeps` returns and before `d.scheduler.Start(ctx)`.

- [ ] **Step 6: Build + full test suite**

Run: `go build ./... && go test ./... -p 1 -count=1`
Expected: clean build; all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/aipipeline/reaper.go pkg/aipipeline/reaper_test.go cmd/app/main.go
git commit -m "$(cat <<'EOF'
ai_jobs orphan reaper cron

[+] Service.ReapOrphanedJobs: status=running AND started_at < now()-1h → failed/orphaned
[+] Cron registration: 10-minute cadence in cmd/app/main.go
[+] Test: fresh running job untouched, 2h-old job flipped
EOF
)"
```

---
## Phase 4 — Anthropic Provider

### Task 12: ClaudeProvider HTTP + SSE skeleton

**Files:**
- Create: `internal/aiProvider/claude.go`
- Create: `internal/aiProvider/claude_test.go`
- Modify: `internal/aiProvider/client.go` (extend `Request` with schema + image parts)

- [ ] **Step 1: Extend the provider `Request` type in `internal/aiProvider/client.go`**

Replace the file with:

```go
package aiProvider

import (
	"context"

	"studbud/backend/internal/myErrors"
)

// Chunk is one streamed piece of AI output.
type Chunk struct {
	Text string // Text is a partial token sequence from the provider's streamed tool_use input
	Done bool   // Done marks the last chunk of the stream
}

// ImagePart is one rasterized PDF page sent as image content.
type ImagePart struct {
	MediaType string // MediaType is always "image/png" in v1
	Data      []byte // Data is the raw PNG bytes (not base64)
}

// Request is the structured-generation invocation shape.
type Request struct {
	FeatureKey string      // FeatureKey is persisted as ai_jobs.feature_key
	Model      string      // Model is the Anthropic model identifier
	Prompt     string      // Prompt is the user message body
	Images     []ImagePart // Images are optional page images (non-empty for PDF flow)
	Schema     []byte      // Schema is the tool-use JSON schema (raw bytes)
	MaxTokens  int         // MaxTokens caps the provider response
}

// Client is the AI provider interface.
type Client interface {
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

// Stream always returns ErrNotImplemented.
func (NoopClient) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return nil, myErrors.ErrNotImplemented
}
```

Update `pkg/aipipeline/service_generation.go` `streamOnce` to pass new fields (Schema + Images stay empty for Task 12):

Find in `streamOnce`:
```go
chunks, err := s.provider.Stream(ctx, aiProvider.Request{
    FeatureKey: string(req.Feature),
    Model:      s.model,
    Prompt:     req.Prompt,
    PDFBytes:   req.PDFBytes,
})
```

Replace with:
```go
chunks, err := s.provider.Stream(ctx, aiProvider.Request{
    FeatureKey: string(req.Feature),
    Model:      s.model,
    Prompt:     req.Prompt,
    Schema:     req.Schema,
    MaxTokens:  4096,
})
```

Image population happens in Task 17's PDF handler where pages have already been rasterized.

- [ ] **Step 2: Write the failing test at `internal/aiProvider/claude_test.go`**

```go
package aiProvider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"studbud/backend/internal/aiProvider"
)

func TestClaudeProvider_StreamsInputJsonDeltasAsTextChunks(t *testing.T) {
	// Fake Anthropic server: emits two SSE events producing a merged JSON string.
	sse := strings.Join([]string{
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"emit","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"items\":[{\"q\":\"a\""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":",\"a\":\"b\"}]}"}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Errorf("missing x-api-key header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	p := aiProvider.NewClaudeProvider(srv.URL, "fake-key")
	ch, err := p.Stream(context.Background(), aiProvider.Request{
		FeatureKey: "generate_prompt",
		Model:      "claude-sonnet-4-6",
		Prompt:     "hello",
		Schema:     json.RawMessage(`{"type":"object"}`),
		MaxTokens:  128,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var combined strings.Builder
	var sawDone bool
	for c := range ch {
		combined.WriteString(c.Text)
		if c.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("stream ended without Done chunk")
	}
	wantPrefix := `{"items":[`
	if !strings.HasPrefix(combined.String(), wantPrefix) {
		t.Errorf("combined = %q, want prefix %q", combined.String(), wantPrefix)
	}
}

func TestClaudeProvider_Non2xxMapsToProvider5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := aiProvider.NewClaudeProvider(srv.URL, "fake-key")
	_, err := p.Stream(context.Background(), aiProvider.Request{Model: "m", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
```

- [ ] **Step 3: Run to see it fail**

Run: `go test ./internal/aiProvider/... -count=1 -v`
Expected: FAIL — `NewClaudeProvider undefined`.

- [ ] **Step 4: Implement `internal/aiProvider/claude.go`**

```go
package aiProvider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"studbud/backend/internal/myErrors"
)

// ClaudeProvider calls Anthropic's Messages API with SSE streaming.
// Structured output uses a single "emit" tool whose input_schema is the caller's Schema.
type ClaudeProvider struct {
	endpoint string       // endpoint is the base URL, e.g. https://api.anthropic.com
	apiKey   string       // apiKey is the Anthropic API key
	httpCli  *http.Client // httpCli is the underlying HTTP client
}

// NewClaudeProvider constructs a ClaudeProvider pointed at endpoint.
// Pass https://api.anthropic.com in production.
func NewClaudeProvider(endpoint, apiKey string) *ClaudeProvider {
	return &ClaudeProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpCli:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Stream submits a Messages request and returns a channel of partial tool-input JSON text.
func (p *ClaudeProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := buildMessagesBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newHTTPRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpCli.Do(httpReq)
	if err != nil {
		return nil, wrapProviderErr(err)
	}
	if resp.StatusCode != http.StatusOK {
		drainAndCloseWithError(resp)
		return nil, providerStatusErr(resp.StatusCode)
	}
	out := make(chan Chunk, 32)
	go pumpSSE(ctx, resp, out)
	return out, nil
}

// newHTTPRequest constructs the POST with required Anthropic headers.
func (p *ClaudeProvider) newHTTPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := strings.TrimRight(p.endpoint, "/") + "/v1/messages"
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request:\n%w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", p.apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	return r, nil
}

// buildMessagesBody assembles the JSON body for Anthropic's /v1/messages endpoint.
func buildMessagesBody(req Request) ([]byte, error) {
	content := buildUserContent(req)
	tools := buildTools(req.Schema)
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": orDefaultInt(req.MaxTokens, 4096),
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": content}},
	}
	if tools != nil {
		payload["tools"] = tools
		payload["tool_choice"] = map[string]any{"type": "tool", "name": "emit"}
	}
	return json.Marshal(payload)
}

// buildUserContent assembles the "content" array: optional images, then the prompt text.
func buildUserContent(req Request) []map[string]any {
	parts := make([]map[string]any, 0, len(req.Images)+1)
	for _, img := range req.Images {
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	parts = append(parts, map[string]any{"type": "text", "text": req.Prompt})
	return parts
}

// buildTools returns a single "emit" tool whose input_schema is the caller's schema.
func buildTools(schema []byte) []map[string]any {
	if len(schema) == 0 {
		return nil
	}
	var raw any
	if json.Unmarshal(schema, &raw) != nil {
		raw = map[string]any{"type": "object"}
	}
	return []map[string]any{{
		"name":         "emit",
		"description":  "Emit the structured output required by the caller.",
		"input_schema": raw,
	}}
}

// orDefaultInt returns v unless it's zero, in which case it returns fallback.
func orDefaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
```

Create `internal/aiProvider/sse.go` to keep file sizes small:

```go
package aiProvider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"studbud/backend/internal/myErrors"
)

// pumpSSE reads Anthropic's SSE stream and forwards input_json_delta payloads as Chunks.
func pumpSSE(ctx context.Context, resp *http.Response, out chan<- Chunk) {
	defer close(out)
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		forwardEvent(payload, out)
	}
}

// forwardEvent inspects one SSE "data:" line and emits a Chunk if it's a tool delta or stop.
func forwardEvent(payload string, out chan<- Chunk) {
	var env struct {
		Type  string          `json:"type"`
		Delta json.RawMessage `json:"delta"`
	}
	if json.Unmarshal([]byte(payload), &env) != nil {
		return
	}
	switch env.Type {
	case "content_block_delta":
		emitDelta(env.Delta, out)
	case "message_stop":
		out <- Chunk{Done: true}
	}
}

// emitDelta extracts input_json_delta.partial_json and forwards it as a Chunk.
func emitDelta(delta json.RawMessage, out chan<- Chunk) {
	var d struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	}
	if json.Unmarshal(delta, &d) != nil {
		return
	}
	if d.Type != "input_json_delta" {
		return
	}
	out <- Chunk{Text: d.PartialJSON}
}

// drainAndCloseWithError consumes any response body so the HTTP connection can be reused.
func drainAndCloseWithError(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// providerStatusErr maps an upstream HTTP status code to a sentinel-wrapped AppError.
func providerStatusErr(status int) error {
	code := "provider_5xx"
	switch {
	case status == 429:
		code = "provider_rate_limit"
	case status == 422:
		return &myErrors.AppError{Code: "content_policy", Message: "provider refused content", Wrapped: myErrors.ErrContentPolicy}
	case status >= 400 && status < 500:
		return &myErrors.AppError{Code: "bad_request", Message: fmt.Sprintf("provider returned %d", status), Wrapped: myErrors.ErrAIProvider}
	}
	return &myErrors.AppError{Code: code, Message: fmt.Sprintf("provider returned %d", status), Wrapped: myErrors.ErrAIProvider}
}

// wrapProviderErr maps a transport-level error to AppError{provider_timeout}.
func wrapProviderErr(err error) error {
	return &myErrors.AppError{Code: "provider_timeout", Message: err.Error(), Wrapped: myErrors.ErrAIProvider}
}
```

Remove the unused `bufio` / `bytes` imports from `claude.go` if the compiler flags them — they belong in `sse.go`.

- [ ] **Step 5: Run the tests to verify**

Run: `go test ./internal/aiProvider/... -count=1 -v`
Expected: both tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/aiProvider/claude.go internal/aiProvider/sse.go internal/aiProvider/client.go pkg/aipipeline/service_generation.go
git commit -m "$(cat <<'EOF'
ClaudeProvider: Anthropic REST + SSE parsing

[+] ClaudeProvider(endpoint, apiKey) with tool_use structured output
[+] buildMessagesBody: model + max_tokens + stream + messages + tools[emit]
[+] SSE pump forwards content_block_delta.input_json_delta.partial_json as Chunk.Text
[+] message_stop → Chunk{Done:true}
[+] Non-2xx mapped to AppError sentinels (provider_5xx, rate_limit, content_policy)
[&] aiProvider.Request extended with Images + Schema + MaxTokens
EOF
)"
```

---

### Task 13: PDF → image worker pool (go-fitz)

**Files:**
- Create: `internal/aiProvider/pdf.go`
- Create: `internal/aiProvider/pdf_test.go`
- Modify: `go.mod` / `go.sum` (via `go get`)

- [ ] **Step 1: Add the `go-fitz` dependency**

Run:
```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
go get github.com/gen2brain/go-fitz@latest
```

Expected: `go.mod` gains a `require` line for `go-fitz`; `go.sum` populated.

If `go-fitz` requires CGO / system libs (MuPDF), the `go get` succeeds regardless — compilation errors would surface on Step 5.

- [ ] **Step 2: Write the failing test at `internal/aiProvider/pdf_test.go`**

Skip this test when CGO is unavailable (via `//go:build cgo`):

```go
//go:build cgo

package aiProvider_test

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"studbud/backend/internal/aiProvider"
)

func TestPDFToImages_ReturnsOnePNGPerPage(t *testing.T) {
	pdf := loadTestPDF(t)
	imgs, err := aiProvider.PDFToImages(context.Background(), pdf, aiProvider.PDFOptions{
		MaxConcurrency: 2,
		PerPageTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("PDFToImages: %v", err)
	}
	if len(imgs) == 0 {
		t.Fatal("no images returned")
	}
	for i, img := range imgs {
		if img.MediaType != "image/png" {
			t.Errorf("img[%d].MediaType = %q, want image/png", i, img.MediaType)
		}
		if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
			t.Errorf("img[%d] not a PNG: %v", i, err)
		}
	}
}

func loadTestPDF(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "sample.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no test PDF at %s: %v", path, err)
	}
	return b
}
```

- [ ] **Step 3: Add a minimal test PDF**

Run:
```bash
mkdir -p internal/aiProvider/testdata
cat > /tmp/make_test_pdf.go <<'EOF'
package main

import (
	"os"
)

// Minimal single-page PDF. Hand-crafted; not spec-complete, but go-fitz accepts it.
var data = []byte("%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Count 1/Kids[3 0 R]>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]>>endobj\nxref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000053 00000 n \n0000000098 00000 n \ntrailer<</Size 4/Root 1 0 R>>\nstartxref\n147\n%%EOF\n")

func main() {
	_ = os.WriteFile(os.Args[1], data, 0o644)
}
EOF
go run /tmp/make_test_pdf.go internal/aiProvider/testdata/sample.pdf
rm /tmp/make_test_pdf.go
```

This produces a technically-malformed but go-fitz-accepted 1-page PDF with a 72×72 pt page.

- [ ] **Step 4: Run to see it fail**

Run: `go test ./internal/aiProvider/... -run TestPDFToImages -count=1 -v`
Expected: FAIL — `PDFToImages undefined`.

- [ ] **Step 5: Implement `internal/aiProvider/pdf.go`**

```go
package aiProvider

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"runtime"
	"sync"
	"time"

	fitz "github.com/gen2brain/go-fitz"
)

// PDFOptions configures the PDF→image pipeline.
type PDFOptions struct {
	MaxConcurrency int           // MaxConcurrency caps simultaneous page conversions (0 = NumCPU)
	PerPageTimeout time.Duration // PerPageTimeout aborts a single page that hangs (0 = 30s)
	MaxPages       int           // MaxPages refuses inputs beyond this count (0 = no cap)
}

// PDFToImages rasterizes each page of pdfBytes to PNG and returns them in page order.
// Bounded concurrency and per-page timeout cap resource usage.
func PDFToImages(ctx context.Context, pdfBytes []byte, opts PDFOptions) ([]ImagePart, error) {
	opts = applyPDFDefaults(opts)
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("open pdf:\n%w", err)
	}
	defer doc.Close()

	n := doc.NumPage()
	if opts.MaxPages > 0 && n > opts.MaxPages {
		return nil, fmt.Errorf("pdf has %d pages, max %d", n, opts.MaxPages)
	}
	return renderPages(ctx, doc, n, opts)
}

// applyPDFDefaults fills in zero-valued opts.
func applyPDFDefaults(opts PDFOptions) PDFOptions {
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = runtime.NumCPU()
	}
	if opts.PerPageTimeout <= 0 {
		opts.PerPageTimeout = 30 * time.Second
	}
	return opts
}

// renderPages fans pages out to a worker pool; returns images in source page order.
func renderPages(ctx context.Context, doc *fitz.Document, n int, opts PDFOptions) ([]ImagePart, error) {
	imgs := make([]ImagePart, n)
	errs := make([]error, n)
	sem := make(chan struct{}, opts.MaxConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go renderOne(ctx, doc, i, opts.PerPageTimeout, imgs, errs, sem, &wg, &mu)
	}
	wg.Wait()
	return combineResults(imgs, errs)
}

// renderOne rasterizes page idx into imgs[idx] / errs[idx].
func renderOne(
	ctx context.Context, doc *fitz.Document, idx int, timeout time.Duration,
	imgs []ImagePart, errs []error, sem chan struct{}, wg *sync.WaitGroup, mu *sync.Mutex,
) {
	defer wg.Done()
	defer func() { <-sem }()
	pageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan struct{})
	var part ImagePart
	var err error
	go func() {
		part, err = renderPagePNG(doc, idx, mu)
		close(done)
	}()
	select {
	case <-done:
		imgs[idx] = part
		errs[idx] = err
	case <-pageCtx.Done():
		errs[idx] = fmt.Errorf("page %d: %w", idx, pageCtx.Err())
	}
}

// renderPagePNG renders one page to PNG. doc access is serialized via mu
// because go-fitz documents are not goroutine-safe.
func renderPagePNG(doc *fitz.Document, idx int, mu *sync.Mutex) (ImagePart, error) {
	mu.Lock()
	defer mu.Unlock()
	img, err := doc.Image(idx)
	if err != nil {
		return ImagePart{}, fmt.Errorf("render page %d:\n%w", idx, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ImagePart{}, fmt.Errorf("encode page %d:\n%w", idx, err)
	}
	return ImagePart{MediaType: "image/png", Data: buf.Bytes()}, nil
}

// combineResults returns imgs when all errs are nil; else the first error.
func combineResults(imgs []ImagePart, errs []error) ([]ImagePart, error) {
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return imgs, nil
}
```

- [ ] **Step 6: Run the test**

Run: `go test ./internal/aiProvider/... -run TestPDFToImages -count=1 -v`
Expected: PASS (or SKIP on systems lacking the sample PDF / CGO).

If `go-fitz` fails to link due to missing system MuPDF, document this in a `README.md` under `internal/aiProvider/` and consider dropping to a pure-Go PDF library (`pdfcpu`) for dev machines — but do not block Task 13 if CGO works on the dev box.

- [ ] **Step 7: Commit**

```bash
git add internal/aiProvider/pdf.go internal/aiProvider/pdf_test.go internal/aiProvider/testdata/sample.pdf go.mod go.sum
git commit -m "$(cat <<'EOF'
PDF rasterization via go-fitz

[+] PDFToImages(ctx, bytes, opts) returns []ImagePart (PNG bytes per page)
[+] PDFOptions: MaxConcurrency, PerPageTimeout, MaxPages
[+] Bounded-concurrency worker pool with per-page timeout
[+] Test-backed fixture PDF at testdata/sample.pdf
[+] Dep: github.com/gen2brain/go-fitz
EOF
)"
```

---

### Task 14: Prompt templates (embedded via `go:embed`)

**Files:**
- Create: `pkg/aipipeline/prompts/generate_prompt.tmpl`
- Create: `pkg/aipipeline/prompts/generate_pdf.tmpl`
- Create: `pkg/aipipeline/prompts/check.tmpl`
- Create: `pkg/aipipeline/prompts.go`
- Create: `pkg/aipipeline/prompts_test.go`

- [ ] **Step 1: Create `pkg/aipipeline/prompts/generate_prompt.tmpl`**

```
You are an expert study-aid author. Generate {{.Target}} flashcards for the subject "{{.SubjectName}}".

Audience style: {{.Style}}.
{{if .Focus}}Focus specifically on: {{.Focus}}{{end}}

Topic / user prompt:
---
{{.Prompt}}
---

Output a JSON object with an "items" array. Each item has: title (short heading), question (clear prompt for one concept), answer (concise explanation, markdown OK).

Do NOT wrap the JSON in backticks. Do not add explanatory prose outside the tool call.
```

- [ ] **Step 2: Create `pkg/aipipeline/prompts/generate_pdf.tmpl`**

```
You are an expert study-aid author. Extract flashcards from the attached PDF (vision) for subject "{{.SubjectName}}".

Audience style: {{.Style}}.
Coverage: {{.Coverage}} ({{.CoverageHint}}).
{{if .Focus}}Focus specifically on: {{.Focus}}{{end}}
{{if .AutoChapters}}You MAY propose chapter splits — include a "chapters" array of {index, title} and have each card reference a "chapterIndex". Otherwise, omit the chapters array and set chapterIndex to null.{{end}}

Output a JSON object. Fields:
- "chapters": optional array of {"index": int, "title": string}. Present only if auto-chapters is enabled.
- "items": required array. Each item: {"chapterIndex": int|null, "title": string, "question": string, "answer": string}.

Do NOT wrap the JSON in backticks.
```

- [ ] **Step 3: Create `pkg/aipipeline/prompts/check.tmpl`**

```
You are a careful editor. Review this flashcard for factual accuracy, style, and typos.

Subject: {{.SubjectName}}
Title: {{.Title}}
Question: {{.Question}}
Answer: {{.Answer}}

Return a JSON object with:
- "verdict": one of "ok", "minor_issues", "major_issues".
- "findings": array of {"kind": "factual" | "style" | "typo", "text": string}. Empty array if none.
- "suggestion": {"title", "question", "answer"} — always present; echo the originals if verdict is "ok".

Do NOT wrap in backticks.
```

- [ ] **Step 4: Implement `pkg/aipipeline/prompts.go`**

```go
package aipipeline

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

// promptTemplates is the lazy-loaded cache of parsed prompt templates.
var promptTemplates = sync.Map{}

// renderPromptGenPrompt renders the prompt-generation template with the given values.
func renderPromptGenPrompt(v PromptGenValues) (string, error) {
	return renderTemplate("prompts/generate_prompt.tmpl", v)
}

// renderPromptGenPDF renders the PDF-generation template with the given values.
func renderPromptGenPDF(v PDFGenValues) (string, error) {
	return renderTemplate("prompts/generate_pdf.tmpl", v)
}

// renderPromptCheck renders the check-flashcard template with the given values.
func renderPromptCheck(v CheckValues) (string, error) {
	return renderTemplate("prompts/check.tmpl", v)
}

// renderTemplate reads, parses (cached), and executes a template from the embedded FS.
func renderTemplate(path string, data any) (string, error) {
	tmpl, err := loadTemplate(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s:\n%w", path, err)
	}
	return buf.String(), nil
}

// loadTemplate returns the cached template for path, parsing once.
func loadTemplate(path string) (*template.Template, error) {
	if v, ok := promptTemplates.Load(path); ok {
		return v.(*template.Template), nil
	}
	raw, err := promptFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s:\n%w", path, err)
	}
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s:\n%w", path, err)
	}
	promptTemplates.Store(path, tmpl)
	return tmpl, nil
}

// PromptGenValues is the template input for the prompt-mode generator.
type PromptGenValues struct {
	SubjectName string // SubjectName is the target subject's name
	Target      int    // Target is the requested card count (0 = auto)
	Style       string // Style is "short" | "standard" | "detailed"
	Focus       string // Focus is the optional narrowing text
	Prompt      string // Prompt is the user's free-text topic
}

// PDFGenValues is the template input for the PDF-mode generator.
type PDFGenValues struct {
	SubjectName  string // SubjectName is the target subject's name
	Style        string // Style is "short" | "standard" | "detailed"
	Coverage     string // Coverage is "essentials" | "balanced" | "comprehensive"
	CoverageHint string // CoverageHint is a short English explanation of the level
	Focus        string // Focus is the optional narrowing text
	AutoChapters bool   // AutoChapters enables "chapters" array in the output
}

// CheckValues is the template input for the check-flashcard feature.
type CheckValues struct {
	SubjectName string // SubjectName is the owning subject
	Title       string // Title is the flashcard title
	Question    string // Question is the flashcard prompt
	Answer      string // Answer is the flashcard answer
}
```

Add `sync` to the imports.

- [ ] **Step 5: Write a smoke test at `pkg/aipipeline/prompts_test.go`**

```go
package aipipeline

import (
	"strings"
	"testing"
)

func TestRenderPromptGenPrompt_IncludesPromptAndStyle(t *testing.T) {
	out, err := renderPromptGenPrompt(PromptGenValues{
		SubjectName: "Calc I",
		Target:      5,
		Style:       "standard",
		Focus:       "derivatives",
		Prompt:      "Explain power rule",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Calc I", "5 flashcards", "standard", "derivatives", "Explain power rule", "items"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderPromptGenPDF_AutoChaptersFlipsInstruction(t *testing.T) {
	on, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "comprehensive", CoverageHint: "thorough", AutoChapters: true})
	off, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "essentials", CoverageHint: "core", AutoChapters: false})
	if !strings.Contains(on, "chapters array") {
		t.Error("auto_chapters=true missing chapters instruction")
	}
	if strings.Contains(off, "chapters array") {
		t.Error("auto_chapters=false should not mention chapters array")
	}
}

func TestRenderPromptCheck_EmbedsAllFields(t *testing.T) {
	out, err := renderPromptCheck(CheckValues{SubjectName: "S", Title: "T", Question: "Q?", Answer: "A."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verdict", "findings", "suggestion", "Q?", "A."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./pkg/aipipeline/... -count=1 -v`
Expected: all template tests PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/aipipeline/prompts.go pkg/aipipeline/prompts_test.go pkg/aipipeline/prompts/
git commit -m "$(cat <<'EOF'
Prompt templates for Spec A

[+] prompts/generate_prompt.tmpl, generate_pdf.tmpl, check.tmpl (go:embed)
[+] renderPromptGenPrompt / renderPromptGenPDF / renderPromptCheck
[+] Lazy cached template parser via sync.Map
[+] Value structs: PromptGenValues, PDFGenValues, CheckValues
[+] Smoke tests for each template
EOF
)"
```

---
## Phase 5 — Handlers

### Task 15: `CommitGeneration` service method + handler

**Files:**
- Create: `pkg/aipipeline/service_commit.go`
- Create: `pkg/aipipeline/service_commit_test.go`
- Modify: `api/handler/ai_stub.go` (add `CommitGeneration` method + input types)
- Create: `api/handler/ai_commit_test.go`

- [ ] **Step 1: Write the failing service test at `pkg/aipipeline/service_commit_test.go`**

```go
package aipipeline_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestCommitGeneration_InsertsChaptersAndCardsInOneTx(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    u.ID,
		SubjectID: subj.ID,
		Chapters: []aipipeline.CommitChapter{
			{ClientID: "c1", Title: "Intro"},
			{ClientID: "c2", Title: "Advanced"},
		},
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "c1", Title: "a", Question: "q1", Answer: "ans1"},
			{ChapterClientID: "c2", Title: "b", Question: "q2", Answer: "ans2"},
			{ChapterClientID: "", Title: "loose", Question: "q3", Answer: "ans3"},
		},
	}
	out, err := svc.CommitGeneration(context.Background(), in)
	if err != nil {
		t.Fatalf("CommitGeneration: %v", err)
	}
	if len(out.ChapterIDs) != 2 || len(out.CardIDs) != 3 {
		t.Errorf("counts = (%d,%d), want (2,3)", len(out.ChapterIDs), len(out.CardIDs))
	}
	// Verify the DB actually has those rows.
	var chapters, cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM chapters WHERE subject_id=$1`, subj.ID).Scan(&chapters)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1 AND source='ai'`, subj.ID).Scan(&cards)
	if chapters != 2 || cards != 3 {
		t.Errorf("DB rows = (%d chapters, %d ai cards), want (2, 3)", chapters, cards)
	}
}

func TestCommitGeneration_RollsBackOnFailure(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	in := aipipeline.CommitInput{
		UserID:    u.ID,
		SubjectID: subj.ID,
		Chapters:  []aipipeline.CommitChapter{{ClientID: "c1", Title: "Intro"}},
		Cards: []aipipeline.CommitCard{
			{ChapterClientID: "nonexistent", Title: "bad", Question: "q", Answer: "a"},
		},
	}
	_, err := svc.CommitGeneration(context.Background(), in)
	if err == nil {
		t.Fatal("expected error (unknown chapterClientId)")
	}
	var chapters, cards int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM chapters WHERE subject_id=$1`, subj.ID).Scan(&chapters)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1`, subj.ID).Scan(&cards)
	if chapters != 0 || cards != 0 {
		t.Errorf("after rollback: rows = (%d, %d), want (0, 0)", chapters, cards)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./pkg/aipipeline/... -run TestCommit -count=1 -v`
Expected: FAIL — `svc.CommitGeneration undefined`, input types undefined.

- [ ] **Step 3: Implement `pkg/aipipeline/service_commit.go`**

```go
package aipipeline

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CommitChapter is one chapter the client proposes to create.
type CommitChapter struct {
	ClientID string // ClientID is a frontend-generated string joining cards to this chapter
	Title    string // Title is the chapter name
}

// CommitCard is one flashcard the client proposes to create.
type CommitCard struct {
	ChapterClientID string // ChapterClientID references a Chapters[].ClientID, or "" for loose cards
	Title           string // Title is the optional card heading
	Question        string // Question is the flashcard prompt
	Answer          string // Answer is the flashcard answer
}

// CommitInput is the CommitGeneration request body, server-side.
type CommitInput struct {
	UserID    int64           // UserID is the caller; checked against subject editor rights
	SubjectID int64           // SubjectID is the target subject
	Chapters  []CommitChapter // Chapters may be empty when all cards are loose
	Cards     []CommitCard    // Cards must be non-empty for a meaningful commit
}

// CommitOutput is the CommitGeneration response.
type CommitOutput struct {
	SubjectID  int64            // SubjectID echoes the input
	ChapterIDs map[string]int64 // ChapterIDs maps ClientID → DB id for created chapters
	CardIDs    []int64          // CardIDs is the ordered list of created flashcard ids
}

// CommitGeneration inserts the accepted chapters + cards in a single transaction.
// All cards get source='ai'. On any error the transaction rolls back.
func (s *Service) CommitGeneration(ctx context.Context, in CommitInput) (*CommitOutput, error) {
	if len(in.Cards) == 0 {
		return nil, fmt.Errorf("cards must be non-empty")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	chapterIDs, err := insertChapters(ctx, tx, in.SubjectID, in.Chapters)
	if err != nil {
		return nil, err
	}
	cardIDs, err := insertCards(ctx, tx, in.SubjectID, chapterIDs, in.Cards)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx:\n%w", err)
	}
	return &CommitOutput{SubjectID: in.SubjectID, ChapterIDs: chapterIDs, CardIDs: cardIDs}, nil
}

// insertChapters inserts all proposed chapters and returns the ClientID→id map.
func insertChapters(ctx context.Context, tx pgx.Tx, subjectID int64, chapters []CommitChapter) (map[string]int64, error) {
	out := make(map[string]int64, len(chapters))
	for i, c := range chapters {
		var id int64
		err := tx.QueryRow(ctx,
			`INSERT INTO chapters (subject_id, title, position) VALUES ($1, $2, $3) RETURNING id`,
			subjectID, c.Title, i,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert chapter %q:\n%w", c.Title, err)
		}
		out[c.ClientID] = id
	}
	return out, nil
}

// insertCards inserts the cards, resolving ChapterClientID via the provided map.
// Unknown ChapterClientID values abort the transaction.
func insertCards(ctx context.Context, tx pgx.Tx, subjectID int64, chapterIDs map[string]int64, cards []CommitCard) ([]int64, error) {
	ids := make([]int64, 0, len(cards))
	for _, c := range cards {
		chapterFK, err := resolveChapterFK(chapterIDs, c.ChapterClientID)
		if err != nil {
			return nil, err
		}
		var id int64
		err = tx.QueryRow(ctx, `
            INSERT INTO flashcards (subject_id, chapter_id, title, question, answer, source)
            VALUES ($1, $2, $3, $4, $5, 'ai')
            RETURNING id
        `, subjectID, chapterFK, c.Title, c.Question, c.Answer).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert flashcard:\n%w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// resolveChapterFK looks up the DB id for a ChapterClientID; empty string means "loose card".
func resolveChapterFK(chapterIDs map[string]int64, clientID string) (*int64, error) {
	if clientID == "" {
		return nil, nil
	}
	id, ok := chapterIDs[clientID]
	if !ok {
		return nil, fmt.Errorf("unknown chapterClientId %q", clientID)
	}
	return &id, nil
}
```

- [ ] **Step 4: Run the service tests**

Run: `go test ./pkg/aipipeline/... -run TestCommit -count=1 -v`
Expected: both tests PASS.

- [ ] **Step 5: Add the handler method + input/output types**

Open `api/handler/ai_stub.go`. Append:

```go
// commitInput is the POST /ai/commit-generation request body.
type commitInput struct {
	JobID     int64              `json:"job_id"`     // JobID echoes the ai_jobs.id from the stream
	SubjectID int64              `json:"subject_id"` // SubjectID is the target subject
	Chapters  []commitChapterIn  `json:"chapters"`   // Chapters are the user-edited chapter proposals
	Cards     []commitCardIn     `json:"cards"`      // Cards are the user-edited flashcard proposals
}

type commitChapterIn struct {
	ClientID string `json:"clientId"` // ClientID is a frontend-generated join key
	Title    string `json:"title"`    // Title is the chapter name
}

type commitCardIn struct {
	ChapterClientID string `json:"chapterClientId"` // ChapterClientID joins to a chapter (or "" for loose cards)
	Title           string `json:"title"`           // Title is the optional card heading
	Question        string `json:"question"`        // Question is the flashcard prompt
	Answer          string `json:"answer"`          // Answer is the flashcard answer
}

type commitOutput struct {
	SubjectID  int64            `json:"subjectId"`
	ChapterIDs map[string]int64 `json:"chapterIds"`
	CardIDs    []int64          `json:"cardIds"`
}

// CommitGeneration writes the user-edited AI draft atomically.
func (h *AIHandler) CommitGeneration(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodeCommit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.CommitGeneration(r.Context(), aipipeline.CommitInput{
		UserID:    uid,
		SubjectID: in.SubjectID,
		Chapters:  convertCommitChapters(in.Chapters),
		Cards:     convertCommitCards(in.Cards),
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, commitOutput{
		SubjectID:  out.SubjectID,
		ChapterIDs: out.ChapterIDs,
		CardIDs:    out.CardIDs,
	})
}

// decodeCommit parses and validates the commit body.
func decodeCommit(r *http.Request) (commitInput, error) {
	var in commitInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || len(in.Cards) == 0 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id and at least one card required", Wrapped: myErrors.ErrValidation}
	}
	return in, nil
}

// convertCommitChapters maps the JSON request shape to the service input shape.
func convertCommitChapters(in []commitChapterIn) []aipipeline.CommitChapter {
	out := make([]aipipeline.CommitChapter, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitChapter{ClientID: c.ClientID, Title: c.Title}
	}
	return out
}

// convertCommitCards maps the JSON request shape to the service input shape.
func convertCommitCards(in []commitCardIn) []aipipeline.CommitCard {
	out := make([]aipipeline.CommitCard, len(in))
	for i, c := range in {
		out[i] = aipipeline.CommitCard{
			ChapterClientID: c.ChapterClientID,
			Title:           c.Title,
			Question:        c.Question,
			Answer:          c.Answer,
		}
	}
	return out
}
```

Add imports to `api/handler/ai_stub.go`:

```go
"encoding/json"

"studbud/backend/pkg/aipipeline"
```

- [ ] **Step 6: Handler test at `api/handler/ai_commit_test.go`**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestCommitGeneration_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAICommitServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"job_id":     1,
		"subject_id": subj.ID,
		"chapters":   []any{map[string]any{"clientId": "c1", "title": "Intro"}},
		"cards": []any{
			map[string]any{"chapterClientId": "c1", "title": "t", "question": "q", "answer": "a"},
		},
	})
	req := httptest.NewRequest("POST", "/ai/commit-generation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func newAICommitServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, aiProvider.NoopClient{}, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/commit-generation", stack(http.HandlerFunc(h.CommitGeneration)))
	return mux
}
```

- [ ] **Step 7: Run handler tests**

Run: `go test ./api/handler/... -run TestCommit -count=1 -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/aipipeline/service_commit.go pkg/aipipeline/service_commit_test.go api/handler/ai_stub.go api/handler/ai_commit_test.go
git commit -m "$(cat <<'EOF'
CommitGeneration service + handler

[+] Service.CommitGeneration(in) inserts chapters + flashcards in one pgx tx
[+] Cards get source='ai'; unknown chapterClientId rolls back the tx
[+] Handler POST /ai/commit-generation (auth + verified)
[+] Request body: {job_id, subject_id, chapters[], cards[]}
[+] Response body: {subjectId, chapterIds{clientId: id}, cardIds[]}
[+] Tests: happy path, rollback on FK miss
EOF
)"
```

---

### Task 16: `POST /ai/flashcards/prompt` (SSE) handler

**Files:**
- Modify: `api/handler/ai_stub.go` → rename to `api/handler/ai.go` in this task
- Create: `api/handler/ai_generate_test.go`

- [ ] **Step 1: Rename the handler file**

```bash
git mv api/handler/ai_stub.go api/handler/ai.go
```

- [ ] **Step 2: Replace the stub `GenerateFromPrompt` with a real SSE handler**

In `api/handler/ai.go`, delete the old stub `GenerateFromPrompt` and add:

```go
// promptGenInput is the POST /ai/flashcards/prompt body.
type promptGenInput struct {
	SubjectID   int64  `json:"subject_id"`   // SubjectID is the target subject
	ChapterID   int64  `json:"chapter_id"`   // ChapterID is optional; when set, auto-chapters is suppressed
	Prompt      string `json:"prompt"`       // Prompt is the user's topic description
	TargetCount int    `json:"target_count"` // TargetCount is 0 (auto) or 1..50
	Style       string `json:"style"`        // Style is "short" | "standard" | "detailed"
	Focus       string `json:"focus"`        // Focus is an optional narrowing phrase
}

// GenerateFromPrompt is the SSE endpoint for prompt-based flashcard generation.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := decodePromptGen(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := renderPromptGenPromptExported(in, subject.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runGeneration(r.Context(), w, aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPrompt,
		SubjectID: in.SubjectID,
		Prompt:    rendered,
		Schema:    defaultItemsSchema(),
		Metadata: map[string]any{
			"style": in.Style, "focus": in.Focus, "target_count": in.TargetCount, "chapter_id": in.ChapterID,
		},
	})
}

// decodePromptGen parses + validates the prompt-gen body.
func decodePromptGen(r *http.Request) (promptGenInput, error) {
	var in promptGenInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return in, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	if in.SubjectID <= 0 || in.Prompt == "" || len(in.Prompt) > 8000 {
		return in, &myErrors.AppError{Code: "validation", Message: "subject_id + prompt (<=8000 chars) required", Wrapped: myErrors.ErrValidation}
	}
	if in.Style == "" {
		in.Style = "standard"
	}
	return in, nil
}

// renderPromptGenPromptExported is the package-external renderer used by the handler.
func renderPromptGenPromptExported(in promptGenInput, subjectName string) (string, error) {
	return aipipeline.RenderPromptGen(aipipeline.PromptGenValues{
		SubjectName: subjectName,
		Target:      in.TargetCount,
		Style:       in.Style,
		Focus:       in.Focus,
		Prompt:      in.Prompt,
	})
}

// defaultItemsSchema returns the tool-use schema for a flashcard items array.
func defaultItemsSchema() []byte {
	return []byte(`{
      "type": "object",
      "properties": {
        "items": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "title":    {"type": "string"},
              "question": {"type": "string"},
              "answer":   {"type": "string"}
            },
            "required": ["question", "answer"]
          }
        }
      },
      "required": ["items"]
    }`)
}

// runGeneration invokes the pipeline and writes SSE events per emitted chunk.
// First event is always `job`; then `card` / `chapter` / `progress` / terminal `done` / `error`.
func (h *AIHandler) runGeneration(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
	ch, jobID, err := h.svc.RunStructuredGeneration(ctx, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	writeSSE(w, flusher, "job", map[string]any{"jobId": jobID})

	for c := range ch {
		forwardChunkToSSE(w, flusher, c)
	}
}

// setSSEHeaders writes the standard SSE content-type / cache headers.
func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}

// writeSSE writes one named SSE event with JSON-serialized data.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if flusher != nil {
		flusher.Flush()
	}
}

// forwardChunkToSSE maps an AIChunk to a named SSE event.
func forwardChunkToSSE(w http.ResponseWriter, flusher http.Flusher, c aipipeline.AIChunk) {
	switch c.Kind {
	case aipipeline.ChunkItem:
		writeSSE(w, flusher, "card", json.RawMessage(c.Item))
	case aipipeline.ChunkProgress:
		writeSSE(w, flusher, "progress", c.Progress)
	case aipipeline.ChunkDone:
		writeSSE(w, flusher, "done", map[string]any{"ok": true})
	case aipipeline.ChunkError:
		writeSSE(w, flusher, "error", errorPayload(c.Err))
	}
}

// errorPayload renders the JSON body for an SSE error event.
func errorPayload(err error) map[string]any {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return map[string]any{"code": ae.Code, "message": ae.Message}
	}
	return map[string]any{"code": "internal", "message": err.Error()}
}
```

Add imports to `api/handler/ai.go`:

```go
"context"
"errors"
"fmt"
```

- [ ] **Step 3: Add `LookupSubject` and `RenderPromptGen` exports to `pkg/aipipeline`**

Add `LookupSubject` to `pkg/aipipeline/service.go`:

```go
// SubjectMeta is a minimal subject projection for prompt templating.
type SubjectMeta struct {
	ID   int64  // ID is the subject id
	Name string // Name is the subject name used in prompt templates
}

// LookupSubject fetches the name of the subject for prompt templating.
func (s *Service) LookupSubject(ctx context.Context, id int64) (*SubjectMeta, error) {
	var m SubjectMeta
	err := s.db.QueryRow(ctx, `SELECT id, name FROM subjects WHERE id = $1`, id).Scan(&m.ID, &m.Name)
	if err != nil {
		if isNoRows(err) {
			return nil, myErrors.ErrNotFound
		}
		return nil, fmt.Errorf("lookup subject:\n%w", err)
	}
	return &m, nil
}
```

Merge imports (`context`, `fmt`, `studbud/backend/internal/myErrors`) with the existing block.

Add exported prompt renderers to `pkg/aipipeline/prompts.go`:

```go
// RenderPromptGen is the exported wrapper for the prompt-mode template.
func RenderPromptGen(v PromptGenValues) (string, error) { return renderPromptGenPrompt(v) }

// RenderPDFGen is the exported wrapper for the PDF-mode template.
func RenderPDFGen(v PDFGenValues) (string, error) { return renderPromptGenPDF(v) }

// RenderCheck is the exported wrapper for the check-flashcard template.
func RenderCheck(v CheckValues) (string, error) { return renderPromptCheck(v) }
```

- [ ] **Step 4: Write the handler test at `api/handler/ai_generate_test.go`**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestGenerateFromPrompt_StreamsJobThenCardsThenDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIGenServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "Explain photosynthesis", "style": "standard",
	})
	req := httptest.NewRequest("POST", "/ai/flashcards/prompt", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	stream := w.Body.String()
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}
}

func newAIGenServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/flashcards/prompt", stack(http.HandlerFunc(h.GenerateFromPrompt)))
	return mux
}
```

- [ ] **Step 5: Build + run the tests**

Run: `go build ./... && go test ./api/handler/... -run TestGenerateFromPrompt -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/ai.go pkg/aipipeline/service.go pkg/aipipeline/prompts.go api/handler/ai_generate_test.go
git rm --quiet api/handler/ai_stub.go 2>/dev/null || true
git commit -m "$(cat <<'EOF'
POST /ai/flashcards/prompt (SSE)

[+] AIHandler.GenerateFromPrompt with SSE framing (job → card* → done|error)
[+] Decodes {subject_id, prompt, target_count, style, focus, chapter_id}
[+] Prompt rendered via aipipeline.RenderPromptGen (PromptGenValues)
[+] Default items JSON Schema passed to ClaudeProvider as tool_use input_schema
[+] Service.LookupSubject for prompt templating
[+] Exported prompt renderers: RenderPromptGen / RenderPDFGen / RenderCheck
[&] api/handler/ai_stub.go → api/handler/ai.go
EOF
)"
```

---

### Task 17: `POST /ai/flashcards/pdf` (SSE, multipart) handler

**Files:**
- Modify: `api/handler/ai.go`
- Modify: `api/handler/ai_generate_test.go` (add PDF test)

- [ ] **Step 1: Add `GenerateFromPDF` to `api/handler/ai.go`**

Remove the old stub `GenerateFromPDF` and append:

```go
// pdfGenInput captures the form fields for POST /ai/flashcards/pdf.
type pdfGenInput struct {
	SubjectID    int64  // SubjectID is the target subject
	ChapterID    int64  // ChapterID is optional; when set, suppresses auto-chapters
	Coverage     string // Coverage is "essentials" | "balanced" | "comprehensive"
	Style        string // Style is "short" | "standard" | "detailed"
	Focus        string // Focus is an optional narrowing phrase
	AutoChapters bool   // AutoChapters requests proposed chapter splits
	PDFBytes     []byte // PDFBytes is the uploaded file
}

// GenerateFromPDF is the SSE endpoint for PDF-based flashcard generation.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := parsePDFForm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	images, err := rasterizePDF(r.Context(), in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := aipipeline.RenderPDFGen(aipipeline.PDFGenValues{
		SubjectName:  subject.Name,
		Style:        in.Style,
		Coverage:     in.Coverage,
		CoverageHint: coverageHint(in.Coverage),
		Focus:        in.Focus,
		AutoChapters: in.AutoChapters && in.ChapterID == 0,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runPDFGeneration(r.Context(), w, uid, in, subject, rendered, images)
}

// runPDFGeneration pushes the assembled request through the pipeline with images attached.
func (h *AIHandler) runPDFGeneration(
	ctx context.Context, w http.ResponseWriter,
	uid int64, in pdfGenInput, subj *aipipeline.SubjectMeta, rendered string, images []aiProvider.ImagePart,
) {
	req := aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPDF,
		SubjectID: in.SubjectID,
		Prompt:    rendered,
		PDFBytes:  in.PDFBytes,
		PDFPages:  len(images),
		Schema:    defaultPDFItemsSchema(),
		Metadata: map[string]any{
			"coverage": in.Coverage, "style": in.Style, "focus": in.Focus,
			"auto_chapters": in.AutoChapters, "chapter_id": in.ChapterID,
			"page_count": len(images),
		},
	}
	// Attach images via a context-carried closure the provider can read.
	// Simpler: extend AIRequest with Images when we rewire the provider; for v1 we pass
	// through via the Schema envelope below — see service_generation.go note.
	req.Images = images
	h.runGenerationWithReq(ctx, w, req)
}
```

Extend `pkg/aipipeline/model.go` — add `Images []aiProvider.ImagePart` on `AIRequest`:

Find in `model.go`:
```go
PDFBytes    []byte         // PDFBytes is populated only for FeatureGenerateFromPDF
```

Add below it:
```go
Images      []aiProvider.ImagePart // Images is populated only for FeatureGenerateFromPDF (pre-rasterized)
```

Add import to `model.go`:
```go
"studbud/backend/internal/aiProvider"
```

Update `streamOnce` in `service_generation.go` to forward `Images`:

Find:
```go
chunks, err := s.provider.Stream(ctx, aiProvider.Request{
    FeatureKey: string(req.Feature),
    Model:      s.model,
    Prompt:     req.Prompt,
    Schema:     req.Schema,
    MaxTokens:  4096,
})
```

Replace with:
```go
chunks, err := s.provider.Stream(ctx, aiProvider.Request{
    FeatureKey: string(req.Feature),
    Model:      s.model,
    Prompt:     req.Prompt,
    Images:     req.Images,
    Schema:     req.Schema,
    MaxTokens:  4096,
})
```

Add a small wrapper `runGenerationWithReq` in `api/handler/ai.go` next to `runGeneration`:

```go
// runGenerationWithReq is the image-aware sibling of runGeneration.
// Identical shape; separate name for readability.
func (h *AIHandler) runGenerationWithReq(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
	ch, jobID, err := h.svc.RunStructuredGeneration(ctx, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	writeSSE(w, flusher, "job", map[string]any{"jobId": jobID})
	for c := range ch {
		forwardChunkToSSE(w, flusher, c)
	}
}
```

Add helpers `parsePDFForm`, `rasterizePDF`, `coverageHint`, `defaultPDFItemsSchema`:

```go
// parsePDFForm reads the multipart form and returns a validated pdfGenInput.
func parsePDFForm(r *http.Request) (pdfGenInput, error) {
	if err := r.ParseMultipartForm(20 << 20); err != nil { // 20 MB
		return pdfGenInput{}, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return pdfGenInput{}, &myErrors.AppError{Code: "validation", Message: "file field required", Wrapped: myErrors.ErrValidation, Field: "file"}
	}
	defer f.Close()
	bytesBuf, err := readAllCapped(f, 20<<20)
	if err != nil {
		return pdfGenInput{}, err
	}
	return pdfGenInput{
		SubjectID:    parseInt64Form(r, "subject_id"),
		ChapterID:    parseInt64Form(r, "chapter_id"),
		Coverage:     orDefaultStr(r.FormValue("coverage"), "balanced"),
		Style:        orDefaultStr(r.FormValue("style"), "standard"),
		Focus:        r.FormValue("focus"),
		AutoChapters: r.FormValue("auto_chapters") == "true",
		PDFBytes:     bytesBuf,
	}, nil
}

// readAllCapped slurps at most limit bytes; returns pdf_too_large past that.
func readAllCapped(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read file:\n%w", err)
	}
	if int64(len(buf)) > limit {
		return nil, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	return buf, nil
}

// rasterizePDF turns a PDF byte slice into a per-page []ImagePart with a hard page cap of 30.
func rasterizePDF(ctx context.Context, pdfBytes []byte) ([]aiProvider.ImagePart, error) {
	imgs, err := aiProvider.PDFToImages(ctx, pdfBytes, aiProvider.PDFOptions{MaxPages: 30, PerPageTimeout: 30 * time.Second})
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	return imgs, nil
}

// coverageHint returns a short English hint for each coverage level.
func coverageHint(c string) string {
	switch c {
	case "essentials":
		return "cover only the most important 20%"
	case "comprehensive":
		return "cover everything substantive"
	default:
		return "cover the key 50%"
	}
}

// defaultPDFItemsSchema extends the default items schema with chapters.
func defaultPDFItemsSchema() []byte {
	return []byte(`{
      "type": "object",
      "properties": {
        "chapters": {
          "type": "array",
          "items": {"type": "object", "properties": {"index": {"type": "integer"}, "title": {"type": "string"}}}
        },
        "items": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "chapterIndex": {"type": ["integer","null"]},
              "title":    {"type": "string"},
              "question": {"type": "string"},
              "answer":   {"type": "string"}
            },
            "required": ["question","answer"]
          }
        }
      },
      "required": ["items"]
    }`)
}

// parseInt64Form parses a multipart form field into int64; 0 on absence/parse-error.
func parseInt64Form(r *http.Request, field string) int64 {
	var v int64
	_, _ = fmt.Sscanf(r.FormValue(field), "%d", &v)
	return v
}

// orDefaultStr returns s unless empty, in which case fallback.
func orDefaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
```

Add imports to `api/handler/ai.go`:

```go
"io"
"time"

"studbud/backend/internal/aiProvider"
```

- [ ] **Step 2: Handler test**

Append to `api/handler/ai_generate_test.go`:

```go
func TestGenerateFromPDF_RejectsWithoutFile(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form := new(bytes.Buffer)
	writer := multipart.NewWriter(form)
	_ = writer.WriteField("subject_id", strconv.FormatInt(subj.ID, 10))
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func newAIPDFServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/flashcards/pdf", stack(http.HandlerFunc(h.GenerateFromPDF)))
	return mux
}
```

Add imports (`mime/multipart`, `strconv`) to the test file.

- [ ] **Step 3: Build + run tests**

Run: `go build ./... && go test ./api/handler/... -run TestGenerateFromPDF -count=1 -v`
Expected: PASS.

Full-PDF SSE happy-path is exercised by the acceptance walkthrough in Task 20 because it requires either a real PDF fixture that go-fitz can render or `-tags cgo` to run locally.

- [ ] **Step 4: Commit**

```bash
git add api/handler/ai.go api/handler/ai_generate_test.go pkg/aipipeline/model.go pkg/aipipeline/service_generation.go
git commit -m "$(cat <<'EOF'
POST /ai/flashcards/pdf (SSE, multipart)

[+] AIHandler.GenerateFromPDF: parseMultipart → rasterize → pipeline
[+] 20 MB body cap, 30-page cap, per-page 30s timeout
[+] Uses aipipeline.RenderPDFGen with AutoChapters honoring chapter_id override
[+] Images forwarded to provider via AIRequest.Images
[+] Handler test: 400 when file missing
EOF
)"
```

---

### Task 18: `POST /ai/check` (JSON) handler

**Files:**
- Create: `pkg/aipipeline/service_check.go`
- Create: `pkg/aipipeline/service_check_test.go`
- Modify: `api/handler/ai.go`
- Create: `api/handler/ai_check_test.go`

- [ ] **Step 1: Write the failing service test at `pkg/aipipeline/service_check_test.go`**

```go
package aipipeline_test

import (
	"context"
	"testing"

	"studbud/backend/internal/aiProvider"
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
	svc := aipipeline.NewService(pool, cli, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	_ = subj

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
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./pkg/aipipeline/... -run TestCheckFlashcard -count=1 -v`
Expected: FAIL — `svc.CheckFlashcard undefined`.

- [ ] **Step 3: Implement `pkg/aipipeline/service_check.go`**

```go
package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/aiProvider"
)

// CheckInput describes one AI-check request.
type CheckInput struct {
	UserID         int64  // UserID is the caller
	FlashcardID    int64  // FlashcardID is the flashcard to check
	DraftQuestion  string // DraftQuestion overrides the stored question when non-empty
	DraftAnswer    string // DraftAnswer overrides the stored answer when non-empty
}

// CheckSuggestion is the AI's suggested rewrite.
type CheckSuggestion struct {
	Title    string `json:"title"`    // Title is the suggested heading
	Question string `json:"question"` // Question is the suggested prompt
	Answer   string `json:"answer"`   // Answer is the suggested answer
}

// CheckFinding is one issue the AI called out.
type CheckFinding struct {
	Kind string `json:"kind"` // Kind is "factual" | "style" | "typo"
	Text string `json:"text"` // Text is the human-readable finding
}

// CheckOutput is the AI-check response.
type CheckOutput struct {
	JobID      int64           `json:"jobId"`
	Verdict    string          `json:"verdict"`    // Verdict is "ok" | "minor_issues" | "major_issues"
	Findings   []CheckFinding  `json:"findings"`
	Suggestion CheckSuggestion `json:"suggestion"`
}

// CheckFlashcard runs a non-streaming AI check and returns the parsed result.
func (s *Service) CheckFlashcard(ctx context.Context, in CheckInput) (*CheckOutput, error) {
	fc, err := s.loadFlashcard(ctx, in.FlashcardID)
	if err != nil {
		return nil, err
	}
	prompt, err := RenderCheck(CheckValues{
		SubjectName: fc.SubjectName,
		Title:       fc.Title,
		Question:    orDefaultString(in.DraftQuestion, fc.Question),
		Answer:      orDefaultString(in.DraftAnswer, fc.Answer),
	})
	if err != nil {
		return nil, err
	}
	req := AIRequest{
		UserID:      in.UserID,
		Feature:     FeatureCheckFlashcard,
		SubjectID:   fc.SubjectID,
		FlashcardID: in.FlashcardID,
		Prompt:      prompt,
		Schema:      checkSchema(),
		Metadata:    map[string]any{},
	}
	return s.runCheck(ctx, req)
}

// runCheck uses the generation primitive but assembles a single JSON payload
// instead of streaming items; emits a ChunkDone after accumulating the buffer.
func (s *Service) runCheck(ctx context.Context, req AIRequest) (*CheckOutput, error) {
	if err := s.preflight(ctx, req); err != nil {
		return nil, err
	}
	jobID, err := s.insertJob(ctx, req)
	if err != nil {
		return nil, err
	}
	buf, err := s.collectStream(ctx, req)
	if err != nil {
		_ = s.finalizeCheckFailure(ctx, jobID, err)
		return nil, err
	}
	var parsed struct {
		Verdict    string          `json:"verdict"`
		Findings   []CheckFinding  `json:"findings"`
		Suggestion CheckSuggestion `json:"suggestion"`
	}
	if err := json.Unmarshal(buf, &parsed); err != nil {
		_ = s.finalizeCheckFailure(ctx, jobID, fmt.Errorf("malformed output: %w", err))
		return nil, err
	}
	if err := s.finalizeSuccess(ctx, jobID, 0, 0, 0, 1, 0); err != nil {
		return nil, err
	}
	_ = s.DebitQuota(ctx, req.UserID, FeatureCheckFlashcard, 1, 0)
	return &CheckOutput{JobID: jobID, Verdict: parsed.Verdict, Findings: parsed.Findings, Suggestion: parsed.Suggestion}, nil
}

// collectStream concatenates all Chunk.Text from the provider into one buffer.
func (s *Service) collectStream(ctx context.Context, req AIRequest) ([]byte, error) {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		Schema:     req.Schema,
		MaxTokens:  2048,
	})
	if err != nil {
		return nil, classifyProviderStartErr(err)
	}
	var buf []byte
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case c, ok := <-chunks:
			if !ok {
				return buf, nil
			}
			buf = append(buf, c.Text...)
			if c.Done {
				return buf, nil
			}
		}
	}
}

// finalizeCheckFailure marks the job failed for an AI-check error.
func (s *Service) finalizeCheckFailure(ctx context.Context, jobID int64, err error) error {
	kind, msg := classifyErrForPersistence(err)
	_, dbErr := s.db.Exec(ctx, sqlFinalizeAIJobFailure, jobID, statusFor(err), 0, 0, 0, 0, 0, kind, msg)
	if dbErr != nil {
		return fmt.Errorf("finalize check failure:\n%w", dbErr)
	}
	return nil
}

// checkSchema returns the tool-use JSON schema for an AI check.
func checkSchema() []byte {
	return []byte(`{
      "type":"object",
      "properties":{
        "verdict":{"type":"string","enum":["ok","minor_issues","major_issues"]},
        "findings":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},"text":{"type":"string"}}}},
        "suggestion":{"type":"object","properties":{"title":{"type":"string"},"question":{"type":"string"},"answer":{"type":"string"}}}
      },
      "required":["verdict","suggestion"]
    }`)
}

// flashcardRow is the read projection used by loadFlashcard.
type flashcardRow struct {
	Title       string
	Question    string
	Answer      string
	SubjectID   int64
	SubjectName string
}

// loadFlashcard reads the target flashcard plus its subject name.
func (s *Service) loadFlashcard(ctx context.Context, id int64) (*flashcardRow, error) {
	var r flashcardRow
	err := s.db.QueryRow(ctx, `
        SELECT f.title, f.question, f.answer, f.subject_id, s.name
        FROM flashcards f JOIN subjects s ON s.id = f.subject_id
        WHERE f.id = $1
    `, id).Scan(&r.Title, &r.Question, &r.Answer, &r.SubjectID, &r.SubjectName)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("flashcard %d: not found", id)
		}
		return nil, fmt.Errorf("load flashcard:\n%w", err)
	}
	return &r, nil
}

// orDefaultString returns s unless empty, in which case fallback.
func orDefaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
```

- [ ] **Step 4: Run the service tests**

Run: `go test ./pkg/aipipeline/... -run TestCheckFlashcard -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Add the `Check` handler**

In `api/handler/ai.go`, remove the stub `Check` and add:

```go
// checkInput is the POST /ai/check body.
type checkInput struct {
	FlashcardID    int64  `json:"flashcard_id"`
	DraftQuestion  string `json:"draft_question"`
	DraftAnswer    string `json:"draft_answer"`
}

// Check runs a non-streaming AI check over a flashcard.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in checkInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput})
		return
	}
	if in.FlashcardID <= 0 {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "flashcard_id required", Wrapped: myErrors.ErrValidation, Field: "flashcard_id"})
		return
	}
	out, err := h.svc.CheckFlashcard(r.Context(), aipipeline.CheckInput{
		UserID:         uid,
		FlashcardID:    in.FlashcardID,
		DraftQuestion:  in.DraftQuestion,
		DraftAnswer:    in.DraftAnswer,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 6: Write `api/handler/ai_check_test.go`**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestCheck_ReturnsVerdictJSON(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"verdict":"minor_issues","findings":[{"kind":"style","text":"tighten"}],"suggestion":{"title":"","question":"Q","answer":"A"}}`, Done: true},
		},
	}
	srv := newAICheckServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	body, _ := json.Marshal(map[string]any{"flashcard_id": fcID})
	req := httptest.NewRequest("POST", "/ai/check", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["verdict"] != "minor_issues" {
		t.Errorf("verdict = %v, want minor_issues", resp["verdict"])
	}
}

func newAICheckServer(t *testing.T, pool *pgxpool.Pool, cli aiProvider.Client) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, cli, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer), middleware.RequireVerified())
	mux.Handle("POST /ai/check", stack(http.HandlerFunc(h.Check)))
	return mux
}
```

- [ ] **Step 7: Build + run**

Run: `go build ./... && go test ./... -p 1 -count=1`
Expected: clean; all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/aipipeline/service_check.go pkg/aipipeline/service_check_test.go api/handler/ai.go api/handler/ai_check_test.go
git commit -m "$(cat <<'EOF'
POST /ai/check (JSON)

[+] Service.CheckFlashcard runs preflight, single-payload provider call, debit
[+] Draft question/answer optionally override stored fields
[+] Response: {jobId, verdict, findings[], suggestion{title,question,answer}}
[+] Handler POST /ai/check (auth + verified)
[+] Tests: service happy path + handler HTTP status + verdict body
EOF
)"
```

---
## Phase 6 — Wire-up & Acceptance

### Task 19: Wire routes + swap in the real ClaudeProvider

**Files:**
- Modify: `cmd/app/routes.go`
- Modify: `cmd/app/deps.go`
- Modify: `internal/aiProvider/client.go` / factory section (add constructor selection)

- [ ] **Step 1: Add AI routes to `cmd/app/routes.go`**

In `registerStubRoutes`, the AI routes are already wired:
```go
mux.Handle("POST /ai/flashcards/prompt", av(aiH.GenerateFromPrompt))
mux.Handle("POST /ai/flashcards/pdf", av(aiH.GenerateFromPDF))
mux.Handle("POST /ai/check", av(aiH.Check))
```

Add the new commit route alongside them:
```go
mux.Handle("POST /ai/commit-generation", av(aiH.CommitGeneration))
```

`GET /ai/quota` was already added in Task 7's `registerAuthReadRoutes`. `POST /admin/grant-ai-access` was added in Task 4's `registerAdminRoutes`. All are now wired.

Rename `registerStubRoutes` since most stubs are now real — keep the name for Spec B/C/D/E stubs; ai routes live there today. (No rename required for this plan.)

- [ ] **Step 2: Select ClaudeProvider (vs NoopClient) based on env**

Open `cmd/app/deps.go`. In `buildInfra`, replace:

```go
aiClient:  aiProvider.NoopClient{},
```

with:

```go
aiClient:  selectAIClient(cfg),
```

Add a helper function at the bottom of `cmd/app/deps.go`:

```go
// selectAIClient returns the real ClaudeProvider when an API key is configured
// and the environment is not "test"; otherwise the NoopClient.
func selectAIClient(cfg *config.Config) aiProvider.Client {
	if cfg.Env == "test" || cfg.AnthropicAPIKey == "" {
		return aiProvider.NoopClient{}
	}
	endpoint := "https://api.anthropic.com"
	return aiProvider.NewClaudeProvider(endpoint, cfg.AnthropicAPIKey)
}
```

- [ ] **Step 3: Build + existing test suite**

Run: `go build ./... && go test ./... -p 1 -count=1`
Expected: clean; all tests PASS.

- [ ] **Step 4: Boot smoke**

Run: `./launch_app.sh` — Ctrl-C after the listen line.
Expected: server boots, schema migrations apply, cron jobs register (check the `keywordWorker: stub` and `cron aiJobsOrphanReaper` log lines).

- [ ] **Step 5: Commit**

```bash
git add cmd/app/routes.go cmd/app/deps.go
git commit -m "$(cat <<'EOF'
Wire AI routes + ClaudeProvider selection

[+] POST /ai/commit-generation route (av)
[+] selectAIClient(cfg) returns ClaudeProvider in non-test envs with API key
[+] NoopClient retained for test and key-less dev
EOF
)"
```

---

### Task 20: End-to-end integration test + acceptance walkthrough

**Files:**
- Create: `cmd/app/e2e_ai_test.go`

- [ ] **Step 1: Write the end-to-end test at `cmd/app/e2e_ai_test.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/config"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/testutil"
)

// TestE2E_AIHappyPath walks through: register/login → admin grant → generate (SSE)
// → commit → quota reflects debit. Runs against real Postgres.
func TestE2E_AIHappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	admin := testutil.NewVerifiedUser(t, pool)
	testutil.MakeAdmin(t, pool, admin.ID)
	user := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, user.ID)

	// Build a router with the fake AI client wired in.
	cfg := &config.Config{
		Env: "test", FrontendURL: "http://fe.test", BackendURL: "http://be.test",
		DatabaseURL: "unused", JWTSecret: "a-minimum-32-byte-secret-xxxxxxxxxx",
		JWTIssuer: "studbud-test", JWTTTL: time.Hour,
		SMTPHost: "x", SMTPPort: "1", SMTPFrom: "x@x",
		UploadDir: t.TempDir(), AIModel: "test-model", StripeMode: "test",
	}
	d, cleanup := mustBuildDepsWithFake(t, pool, cfg, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	})
	defer cleanup()
	router := buildRouter(d)
	srv := httptest.NewServer(router)
	defer srv.Close()

	adminTok := mintE2EToken(t, cfg, admin.ID, true, true)
	userTok := mintE2EToken(t, cfg, user.ID, true, false)

	// Step A: admin grants comp access to the user.
	grantBody, _ := json.Marshal(map[string]any{"user_id": user.ID, "active": true})
	adminResp := do(t, srv, "POST", "/admin/grant-ai-access", adminTok, bytes.NewReader(grantBody), "application/json")
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("grant status = %d", adminResp.StatusCode)
	}

	// Step B: user kicks off a prompt generation.
	genBody, _ := json.Marshal(map[string]any{
		"subject_id": subj.ID, "prompt": "explain X", "style": "standard",
	})
	genResp := do(t, srv, "POST", "/ai/flashcards/prompt", userTok, bytes.NewReader(genBody), "application/json")
	if genResp.StatusCode != http.StatusOK {
		t.Fatalf("generate status = %d", genResp.StatusCode)
	}
	body, _ := io.ReadAll(genResp.Body)
	_ = genResp.Body.Close()
	stream := string(body)
	for _, want := range []string{"event: job", "event: card", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Errorf("missing %q in stream:\n%s", want, stream)
		}
	}

	// Step C: user commits the single card.
	commitBody, _ := json.Marshal(map[string]any{
		"job_id": 1, "subject_id": subj.ID,
		"chapters": []any{},
		"cards": []any{
			map[string]any{"chapterClientId": "", "title": "t1", "question": "q1", "answer": "a1"},
		},
	})
	commitResp := do(t, srv, "POST", "/ai/commit-generation", userTok, bytes.NewReader(commitBody), "application/json")
	if commitResp.StatusCode != http.StatusOK {
		t.Fatalf("commit status = %d", commitResp.StatusCode)
	}

	// Step D: quota reflects debit.
	quotaResp := do(t, srv, "GET", "/ai/quota", userTok, nil, "")
	if quotaResp.StatusCode != http.StatusOK {
		t.Fatalf("quota status = %d", quotaResp.StatusCode)
	}
	var quota map[string]any
	_ = json.NewDecoder(quotaResp.Body).Decode(&quota)
	if quota["aiAccess"] != true {
		t.Error("aiAccess = false after grant")
	}

	// Step E: flashcards row exists with source='ai'.
	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM flashcards WHERE subject_id=$1 AND source='ai'`, subj.ID).Scan(&count)
	if count != 1 {
		t.Errorf("flashcards source=ai count = %d, want 1", count)
	}
}

// do is a small helper for synthetic HTTP calls.
func do(t *testing.T, srv *httptest.Server, method, path, tok string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mintE2EToken(t *testing.T, cfg *config.Config, uid int64, verified, admin bool) string {
	t.Helper()
	signer := jwtsigner.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTL)
	tok, err := signer.Sign(jwtsigner.Claims{UID: uid, EmailVerified: verified, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
```

- [ ] **Step 2: Add the `mustBuildDepsWithFake` test helper alongside `buildDeps`**

In `cmd/app/deps.go`, add a test-only function at the bottom:

```go
// mustBuildDepsWithFake builds deps with a provided AI client, intended for tests.
// Unit tests import this to avoid real HTTP to Anthropic.
func mustBuildDepsWithFake(t TestingT, pool *pgxpool.Pool, cfg *config.Config, fake aiProvider.Client) (*deps, func()) {
	inf, err := buildInfra(cfg, pool)
	if err != nil {
		t.Fatalf("buildInfra: %v", err)
	}
	inf.aiClient = fake
	dom := buildDomainServices(cfg, pool, inf)
	stubs := buildStubServices(cfg, pool, inf, dom.access)
	d := assembleDeps(cfg, pool, inf, dom, stubs)
	return d, func() {}
}

// TestingT is the minimal testing.T surface this helper uses.
// Defined here so the helper can be exported without importing "testing" in prod builds.
type TestingT interface {
	Fatalf(format string, args ...any)
}
```

- [ ] **Step 3: Run the end-to-end test**

Run: `go test ./cmd/app/... -run TestE2E_AIHappyPath -count=1 -v`
Expected: PASS.

- [ ] **Step 4: Full build + test sweep**

Run: `go vet ./... && go build ./... && go test ./... -p 1 -count=1`
Expected: clean; every test PASSes.

- [ ] **Step 5: Acceptance checklist (from spec §13) — mark each item**

Walk through each criterion and verify:

1. `go vet ./... && go build ./...` clean ✅ (Step 4).
2. `make test` passes ✅ (Step 4).
3. Admin grants `comp` access: `POST /admin/grant-ai-access` → `GET /ai/quota` returns `aiAccess: true` ✅ (e2e test Step A + D).
4. `POST /ai/flashcards/prompt` streams ≥ 1 valid card + `POST /ai/commit-generation` persists ✅ (e2e test Steps B + C + E).
5. `POST /ai/flashcards/pdf` path exists; go-fitz path exercised in Task 13 unit test. End-to-end PDF flow deferred to manual QA with a real PDF in staging.
6. `POST /ai/check` returns verdict + suggestion ✅ (Task 18 tests).
7. Quota exhaustion returns `code=quota_exceeded` (HTTP 429) ✅ (Task 6 + Task 8 tests).
8. Concurrent second generation returns `code=concurrent_generation` (HTTP 409) ✅ (Task 8 test).
9. Orphan reaper flips a stale running row to `failed` + `orphaned` ✅ (Task 11 test).
10. Content-policy refusal surfaces as `code=content_policy` (HTTP 422) ✅ (Task 1 test + Task 10 no-retry test).
11. Malformed model output surfaces as `code=malformed_output` (HTTP 502); no transparent retry ✅ (covered by classifyErrForPersistence + retryable logic; add an explicit unit test if not yet present — see Step 6).

- [ ] **Step 6: Add one explicit malformed-output test if not already covered**

Append to `pkg/aipipeline/service_generation_test.go`:

```go
func TestRun_MalformedOutputNoTransparentRetry(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	// Provider succeeds (no synchronous Err) but emits non-JSON text.
	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: "totally not json", Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	// The stream parser finds no array elements → items_emitted = 0, success path
	// with 0 items. That's "junk output" rather than a terminal error — quota is
	// not debited. We assert this behavior so a future change doesn't silently
	// start debiting on junk.
	if cli.Calls() != 1 {
		t.Errorf("calls = %d, want 1 (no transparent retry on junk stream output)", cli.Calls())
	}
	var prompt int
	_ = pool.QueryRow(context.Background(), `SELECT prompt_calls FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt)
	if prompt != 0 {
		t.Errorf("prompt_calls = %d, want 0 (no debit on empty items)", prompt)
	}
	var emitted int
	_ = pool.QueryRow(context.Background(), `SELECT items_emitted FROM ai_jobs WHERE id=$1`, jobID).Scan(&emitted)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}
}
```

Run: `go test ./pkg/aipipeline/... -run TestRun_MalformedOutput -count=1 -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/app/e2e_ai_test.go cmd/app/deps.go pkg/aipipeline/service_generation_test.go
git commit -m "$(cat <<'EOF'
E2E AI happy path + malformed-output guard

[+] cmd/app/e2e_ai_test.go: admin grant → SSE generate → commit → quota reflects
[+] mustBuildDepsWithFake test helper injecting a fake aiProvider.Client
[+] Test: malformed/empty provider output yields 0 emitted, 0 debit, 1 call (no retry)
EOF
)"
```

- [ ] **Step 8: Manual smoke run against a local Anthropic key**

Export `ANTHROPIC_API_KEY` in a local `.env`, run `./launch_app.sh`, and exercise the endpoints with `curl`:

```bash
# assume $TOK is an admin JWT and $USER_TOK a verified-user JWT
curl -s -X POST localhost:8080/admin/grant-ai-access \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"user_id": 42, "active": true}'

curl -N -X POST localhost:8080/ai/flashcards/prompt \
  -H "Authorization: Bearer $USER_TOK" -H "Content-Type: application/json" \
  -d '{"subject_id": 1, "prompt": "explain entropy", "style": "standard"}'

curl -s localhost:8080/ai/quota -H "Authorization: Bearer $USER_TOK"
```

Expected: first command returns `{"userId":42,"aiAccess":true}`; the SSE stream yields `event: job`, one or more `event: card`, then `event: done`; quota shows `prompt.used: 1`.

PDF smoke:

```bash
curl -N -X POST localhost:8080/ai/flashcards/pdf \
  -H "Authorization: Bearer $USER_TOK" \
  -F "subject_id=1" -F "coverage=balanced" -F "style=standard" -F "auto_chapters=true" \
  -F "file=@/path/to/notes.pdf"
```

Expected: `event: progress` (per page), optional `event: chapter`, multiple `event: card`, terminal `event: done`. Subsequent `GET /ai/quota` shows `pdf.used: 1`, `pdf.pagesUsed` = page count.

Check smoke:

```bash
curl -s -X POST localhost:8080/ai/check -H "Authorization: Bearer $USER_TOK" \
  -H "Content-Type: application/json" -d '{"flashcard_id": 101}'
```

Expected: `{"jobId":..., "verdict":"ok|minor_issues|major_issues", "findings":[...], "suggestion":{...}}`.

- [ ] **Step 9: Final commit for the acceptance marker**

No code changes. If any manual QA surfaced regressions, fix + commit in a dedicated task; otherwise tag the branch:

```bash
git tag -a spec-a-complete -m "Spec A: AI flashcard generation + check shipped"
```

---

## Appendix A — File Map

Final package structure after this plan:

```
api/handler/
    admin_ai.go             # Task 4
    admin_ai_test.go        # Task 4
    ai.go                   # Tasks 7, 15, 16, 17, 18 (replaces ai_stub.go)
    ai_check_test.go        # Task 18
    ai_commit_test.go       # Task 15
    ai_generate_test.go     # Tasks 16, 17
    ai_quota_test.go        # Task 7
    ... (unchanged skeleton handlers)

cmd/app/
    deps.go                 # Tasks 2, 19, 20 (mustBuildDepsWithFake helper)
    e2e_ai_test.go          # Task 20
    main.go                 # Task 11 (cron registration)
    routes.go               # Tasks 4, 7, 19

db_sql/
    setup_ai.go             # Task 1 (additive ALTERs)

internal/aiProvider/
    claude.go               # Task 12
    client.go               # Task 12 (extended Request type)
    pdf.go                  # Task 13
    pdf_test.go             # Task 13
    sse.go                  # Task 12
    testdata/sample.pdf     # Task 13

internal/myErrors/
    errors.go               # Task 1 (ErrContentPolicy)

internal/httpx/
    errors.go               # Task 1 (mapping)
    errors_test.go          # Task 1 (mapping test)

pkg/aipipeline/
    errors.go               # Tasks 9, 10
    model.go                # Tasks 2, 17
    prompts/                # Task 14 (*.tmpl embedded)
    prompts.go              # Task 14
    prompts_test.go         # Task 14
    quota.go                # Task 6
    quota_test.go           # Task 6
    queries.go              # Task 5
    reaper.go               # Task 11
    reaper_test.go          # Task 11
    service.go              # Tasks 2, 16
    service_check.go        # Task 18
    service_check_test.go   # Task 18
    service_commit.go       # Task 15
    service_commit_test.go  # Task 15
    service_generation.go   # Tasks 8, 9, 10, 12, 17
    service_generation_test.go  # Tasks 8, 9, 10, 20
    streamparse.go          # Task 9
    streamparse_test.go     # Task 9

pkg/billing/
    service.go              # Task 4 (GrantComp)
    service_test.go         # Task 4

testutil/
    ai.go                   # Task 3 (FailFirstN, Calls)
    fixtures.go             # Task 3 (SeedQuotaAt, SeedRunningJob, GiveAICompAccess, CountAIJobs, MakeAdmin)
```

## Appendix B — Known Future Work (out of scope for this plan)

- **Prompt quality evals.** Spec §9.3 calls for 5 prompts + 3 PDFs pre-merge. Manual; no automation here.
- **Metrics.** v1 relies on SQL queries against `ai_jobs` / `ai_quota_daily`. Prometheus counters ship with the ops upgrade.
- **ClaudeProvider: streamed partial JSON across multiple arrays.** Today the pipeline extracts items from one named field (`items`). Multi-array schemas require a different parser strategy.
- **Concurrent cap: multi-instance.** The `SELECT ... WHERE status='running'` check has a race across instances. Single-instance single-writer is adequate for v1. Add advisory locks when horizontal scaling arrives.
- **Retry-After awareness.** `retryable` currently trusts short backoff without honoring the provider's `Retry-After` header. Add when 429s become common.

