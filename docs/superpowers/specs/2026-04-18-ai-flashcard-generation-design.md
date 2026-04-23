# Spec A — AI Flashcard Generation & AI Check

**Status:** Design approved. Implementation plan to be authored against this revision.
**Date (original):** 2026-04-18
**Date (revised):** 2026-04-23 — reconciled with the StudBud backend skeleton (Postgres + `pgx/v5`, unified `pkg/aipipeline/`, Spec C-compatible entitlement through `user_subscriptions`).
**Scope:** Single spec. Does not cover AI revision planning (Spec B), real subscription billing (Spec C), or downstream AI features (quiz — Spec D, duels — Spec E).

---

## 0. Reconciliation with the Skeleton (2026-04-23)

This document originally targeted an earlier backend (SQLite, a separate `github.com/martonroux/go-study-buddy` repo, an `ai_subscription_active` stub column). The StudBud backend skeleton at `/Users/martonroux/Documents/WEB/studbud_3/backend/` (module `studbud/backend`, Postgres via `pgx/v5`) has since been built and already bakes in several decisions this spec must honor. The substance of the product design — generation UX, AI-check flow, quota model — is unchanged. The mechanical carrier layer is:

| Topic | Old spec | Revised |
|---|---|---|
| Module / repo | `github.com/martonroux/go-study-buddy` | `studbud/backend` at `/Users/martonroux/Documents/WEB/studbud_3/backend/` |
| DB | SQLite (`mattn/go-sqlite3`, `database/sql`) | Postgres (`jackc/pgx/v5`, `pgxpool`) |
| AI package layout | `pkg/ai/`, `pkg/aiJob/`, `api/service/aiService.go`, `api/service/aiQuotaService.go` | Unified `pkg/aipipeline/` (`model.go` / `service.go` / `queries.go` / `errors.go`) |
| Entitlement stub | `users.ai_subscription_active BOOLEAN` | `user_subscriptions` + SQL function `user_has_ai_access(uid)` via `access.Service.HasAIAccess` (Spec C-compatible from day one) |
| Entitlement admin flip | `POST /admin/set-ai-subscription`, guarded by `ADMIN_API_ENABLED=true` | `POST /admin/grant-ai-access`, guarded by `RequireAdmin` middleware + `users.is_admin` |
| Test harness | New `testutil/testdb.go` / `fixtures.go` to bootstrap | Already exists in repo root (`testutil/`) with real Postgres fixtures + `testutil.FakeAIClient` |
| Error model | String `kind` responses | `internal/myErrors` sentinels + `AppError.Code` + `internal/httpx` JSON envelope `{"error":{"code","message","field"}}` |

All schema changes in this spec are **additive `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`** in `db_sql/setup_ai.go`. No migrations, no table rewrites, no destructive changes. The skeleton's stub `pkg/aipipeline/Service` gets its body filled in.

---

## 1. Purpose

Give subscription-entitled StudBud users two AI-powered authoring affordances:

1. **Generate flashcards** from a prompt or from an uploaded PDF, with a preview/edit screen before cards are committed. Optional AI-proposed chapter splits for PDFs.
2. **Check an existing flashcard** with one click from the flashcard editor: receive factual/style/typo findings and a suggested rewrite the user can accept per-field into the editor draft.

Both features run through the StudBud backend, which proxies the provider (Anthropic Claude) so no API key ever ships to the client.

## 2. Product Decisions (Locked)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Sequencing | Spec A (generation + check) ships first. Spec B (revision plan) follows, reusing the AI layer. | AI revision plan depends on an orchestration layer Spec A forces us to build. |
| v1 generation scope | Prompt → FCs + PDF → FCs + "AI check" button in the FC editor. No save-time auto-check. | Covers the 90% ask while keeping cost and UX predictable. |
| Provider location | Backend-proxied. New `/ai/*` endpoints; backend holds the provider key. | Required for mobile (Capacitor) — can't ship keys to clients. |
| Entitlement model | `user_subscriptions` row (`status IN ('active','trialing','comp')`), resolved via SQL function `user_has_ai_access(uid)`. Beta/dev grants use a `comp` row inserted by the admin endpoint; Spec C replaces that write path with Stripe webhooks. | Spec C-compatible from day one. No throwaway `ai_subscription_active` column. |
| Cost control | Per-request hard limits + per-user daily quotas (separate counters for prompt, PDF, PDF-pages, check). No free tier. | PDF has stricter caps than prompt. Tune numbers post-launch. |
| PDF ingestion | Vision multimodal — PDF pages → images → Claude. No text extraction path. | Covers scanned polys, handwriting, diagrams, math uniformly. Subscription caps exposure. |
| Generation inputs | Target subject (+ optional chapter), prompt OR PDF, target count (or auto) for prompt / 3-level **coverage** for PDF, style, focus text, **chapter auto-creation** (PDF only). | Prompt mode lets the user pin a count; PDF mode instead picks how much of the source to cover (`essentials` / `balanced` / `comprehensive`) because card count depends on the PDF. |
| Preview flow | Generated cards land on a staging/review screen. User edits / deletes / reassigns / renames chapters / merges chapters, then "Save all" commits atomically. | Protects the user from hallucinations; no FC versioning exists yet. |
| AI-check output | Verdict (`ok` / `minor_issues` / `major_issues`) + typed findings (factual / style / typo) + a suggested rewrite. | Giving a rewrite is what makes the button actually useful. |
| AI-check UI | Modal with diff view over title / question / answer. Per-field Apply. Apply writes to editor's in-memory draft; user still clicks Save to persist. | Matches the editor's existing save model; keeps suggestions as proposals, not commits. |
| Streaming | Generation: Server-Sent Events (via the skeleton's `httpx.Stream`). Check: non-streaming. | Streaming pays off for multi-card generation. Small single-shot responses gain nothing from SSE. |
| Error model | Partial results preserved on failure. One transparent backend retry on transient **transport** errors (5xx / timeout / short 429). No transparent retry on malformed output, content policy, 4xx, or context cancellation — each surfaces to the user, who can manually retry if it makes sense. | Malformed output repeats expensively; users should pay (with a click) for a re-attempt. |
| Architecture | Single generic AI pipeline (`Service.RunStructuredGeneration`) + thin per-feature handlers. | Entitlement, quota, retry, audit, SSE framing, schema validation all live in one place. Spec B reuses it unchanged. |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`studbud/backend`):**

- **`pkg/aipipeline/`** — the pipeline package. Unified replacement for the old `pkg/ai/` + `pkg/aiJob/` + `api/service/aiService.go` + `api/service/aiQuotaService.go` split. Follows the skeleton's uniform per-domain layout:
  - `model.go` — `FeatureKey`, `AIRequest`, `AIChunk`, `ChunkKind`, `AIJob` row struct, `QuotaSnapshot`, `QuotaLimits`, `DefaultQuotaLimits()`.
  - `service.go` (split into `service_generation.go` / `service_check.go` / `service_quota.go` / `service_commit.go` if any file crosses ~300 lines) — `Service` methods: `RunStructuredGeneration`, `Check`, `CommitGeneration`, `GetQuota`, `GrantAIAccess`.
  - `queries.go` — raw SQL as `const` strings.
  - `errors.go` — re-exports the relevant sentinels from `internal/myErrors` and provides helper constructors (`newContentPolicyErr`, `newMalformedOutputErr`, etc.) that set `AppError.Code`.
  - `prompts/` — `//go:embed` text templates: `generate_prompt.tmpl`, `generate_pdf.tmpl`, `check.tmpl`.
- **`internal/aiProvider/`** — narrow `Client` interface (already present with `NoopClient`). New concrete `ClaudeProvider` implements Anthropic REST + SSE parsing, transparent-retry backoff, PDF → image via `go-fitz` with a bounded-concurrency worker pool + per-page timeout. Only module that imports Anthropic.
- **`api/handler/ai.go`** — thin handlers, replaces the existing `ai_stub.go`. Methods: `GenerateFromPrompt` (SSE), `GenerateFromPDF` (SSE, multipart), `Check` (JSON), `CommitGeneration` (JSON), `Quota` (JSON).
- **`api/handler/admin_ai.go`** — new. One method: `GrantAIAccess`. Wired under `RequireAdmin` middleware.
- **`internal/http/middleware/admin.go`** — new `RequireAdmin` middleware (checks `users.is_admin=true` after `Auth`). Usable across future admin routes.
- **`internal/cron/`** — new job registration: `aiJobsOrphanReaper` (every 10 min). Alongside the skeleton's existing `ai_quota_daily` GC and `ai_extraction_jobs` stale-claim reclaim.

**Frontend (`/Users/martonroux/Documents/WEB/studbud_3/studbud/src/`):**

- `api/ai.ts` — SSE-aware client for generation; plain `fetch` for check / quota.
- `stores/ai.ts` — Pinia store: current generation job (streamed draft cards + proposed chapters + progress + error), AI-check modal state, cached quota snapshot.
- `pages/FlashcardGeneratePage.vue` — input form (`/subjects/:id/generate`, `/chapters/:id/generate`).
- `pages/FlashcardGenerateReviewPage.vue` — staging/preview/commit screen (`/subjects/:id/generate/review`).
- `components/ai/AiGenerationControls.vue` — count / style / focus / coverage / auto-chapters toggle.
- `components/ai/AiCheckModal.vue` — diff + per-field Apply.
- `components/ai/QuotaBadge.vue` — remaining quota indicator.
- `components/ai/PaywallCard.vue` — placeholder shown when entitlement is off.
- Entry points: "Generate with AI" button on subject-detail and chapter-detail; "Check with AI" button in the flashcard editor.

### 3.2 Hard boundaries

- Frontend NEVER talks directly to the provider.
- `Service.RunStructuredGeneration` is the ONLY function that invokes `aiProvider.Client.Stream`. Any new AI feature (Spec B revision plan, Spec D quiz) registers a `FeatureKey` and reuses it.
- Entitlement (`access.Service.HasAIAccess`) + quota (internal `debit` / `check` on `ai_quota_daily`) are enforced **inside** the pipeline. Handlers cannot forget.
- Prompts live in versioned template files under `pkg/aipipeline/prompts/` (embedded via `//go:embed`), never as inline strings in handlers.

## 4. The AI Pipeline Primitive

Located in `pkg/aipipeline/service_generation.go`. Handlers pass messages + a JSON schema + a feature key; the pipeline returns a channel of validated, typed chunks.

```go
type FeatureKey string

const (
    FeatureGenerateFromPrompt FeatureKey = "generate_prompt"
    FeatureGenerateFromPDF    FeatureKey = "generate_pdf"
    FeatureCheckFlashcard     FeatureKey = "check_flashcard"
)

type AIRequest struct {
    UserID      int64                 // BIGINT from users.id
    Feature     FeatureKey
    SubjectID   int64                 // required; FK into ai_jobs.subject_id
    FlashcardID int64                 // 0 unless Feature == FeatureCheckFlashcard
    Messages    []aiProvider.Message  // role + parts (text or image)
    Schema      JSONSchema            // expected item shape
    MaxTokens   int
    PDFPages    int                   // 0 for non-PDF features
    Metadata    map[string]any        // style / focus / coverage / target_count — persisted to ai_jobs.metadata
}

type ChunkKind string
const (
    ChunkItem     ChunkKind = "item"
    ChunkProgress ChunkKind = "progress"
    ChunkDone     ChunkKind = "done"
    ChunkError    ChunkKind = "error"
)

type AIChunk struct {
    Kind     ChunkKind
    Item     json.RawMessage       // one schema-validated item (for streamed arrays)
    Progress *ProgressInfo         // {"phase":"analyzing","page":3,"total":12}
    Err      error                 // concrete *myErrors.AppError on ChunkError
}

// Returns the channel, the new ai_jobs row ID, and a synchronous error
// for pre-stream failures (entitlement, quota, concurrent cap, bad request).
func (s *Service) RunStructuredGeneration(
    ctx context.Context, req AIRequest,
) (<-chan AIChunk, int64, error)
```

### 4.1 Pipeline steps, in order

1. **Entitlement check.** `access.HasAIAccess(ctx, uid)` → `ErrNoAIAccess` (HTTP 402) on false.
2. **Quota check.** Read today's `ai_quota_daily` row (column `day = CURRENT_DATE`). If the relevant counter is at the per-feature limit → `ErrQuotaExhausted` (HTTP 429); the `AppError.Message` carries a human-readable reset hint ("resets at midnight UTC").
3. **Concurrent-generation cap** (generate features only). `SELECT 1 FROM ai_jobs WHERE user_id=$1 AND status='running' AND feature_key IN ('generate_prompt','generate_pdf') LIMIT 1`. Any hit → `ErrConflict` (HTTP 409). `AppError.Message` includes the existing `jobId` so the client can resume or dedupe.
4. **PDF-pages upfront debit** (PDF feature only). Pages are consumed whether cards are accepted or not: `UPDATE ai_quota_daily SET pdf_pages = pdf_pages + $pages ...` before the provider call. If the debit would exceed the `pdf_pages` limit, surface `ErrQuotaExhausted` here (pre-provider).
5. **Insert `ai_jobs` row** with `status='running'`, populated fields (`subject_id`, `flashcard_id`, `pdf_page_count`, `feature_key`, `model`, `metadata`), `started_at=now()`. Return the new `id` to the handler.
6. **Provider stream.** Call `aiProvider.Client.Stream(ctx, ...)`. **Retry policy:**
    - One transparent retry on transport transients (5xx, timeout, 429 with `Retry-After < 5s`), short jittered backoff.
    - **No transparent retry** on: malformed output / schema violation, content-policy refusal, 4xx, `ctx.Cancel()`, 401. Each surfaces immediately with a distinct `AppError.Code` so the client can offer a user-initiated retry where it makes sense.
7. **Incremental JSON validation.** Streaming parser consumes provider tokens; each complete item is validated against `Schema`. Valid → emit `ChunkItem`. Invalid → drop, warn-log, increment `items_dropped` on the job row. Don't abort the stream.
8. **Quota debit per emitted item** for the feature's "calls" counter? **No** — the calls counters increment once per successful job (see §6). `items_emitted` on the job row tracks per-card counts for audit only.
9. **Terminal event + job finalization.** On stream close, error, or `ctx.Cancel()`, always `UPDATE ai_jobs SET status=?, finished_at=now(), input_tokens=?, output_tokens=?, cents_spent=?, items_emitted=?, items_dropped=?, error=?, error_kind=? WHERE id=?`. Debit the feature "calls" counter only when `status='complete' AND items_emitted > 0`.

### 4.2 What the pipeline does NOT do

- Doesn't know what a flashcard is. Schema + messages come from handlers.
- Doesn't know SSE framing — the handler adapts `<-chan AIChunk` to `httpx.Stream`.
- Doesn't retry beyond one transparent attempt on transport errors. The user re-triggers from the UI for anything else.
- Doesn't persist prompt content, output content, or uploaded PDF bytes — `ai_jobs` carries only counts and `metadata`.

### 4.3 Provider abstraction

`internal/aiProvider.Client` is already the skeleton's interface:

```go
type Client interface {
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
```

For Spec A the concrete impl is `ClaudeProvider` (vision-only for PDFs, no text fallback in v1). PDF → image lives in this package, behind a bounded-concurrency worker pool with per-page timeout. Anthropic-specific details (endpoint, SSE event names, content-block kinds) never leak outside `internal/aiProvider/`.

Tests swap `Client` for `testutil.FakeAIClient` (already in `testutil/ai.go`).

## 5. Data Model Changes

### 5.1 `ai_jobs` — additive ALTERs (in `db_sql/setup_ai.go`)

The skeleton created `ai_jobs` with `id, user_id, feature_key, model, input_tokens, output_tokens, cents_spent, status, error, metadata JSONB, started_at, finished_at`. Spec A adds typed columns for the observability fields the pipeline needs, keeping everything else in `metadata`:

```sql
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS subject_id      BIGINT NULL
    REFERENCES subjects(id) ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS flashcard_id    BIGINT NULL
    REFERENCES flashcards(id) ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS pdf_page_count  INT NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_emitted   INT NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_dropped   INT NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS error_kind      TEXT NULL;
CREATE INDEX IF NOT EXISTS idx_ai_jobs_user_running
    ON ai_jobs(user_id) WHERE status = 'running';
```

Dropped (vs. old spec): `input_hash`, `provider`, `provider_req_id`, `request_params` (latter lives in `metadata`). `input_hash` was never consumed; `provider`/`provider_req_id` are niceties not worth the column budget.

**Status values:** `'running' | 'complete' | 'failed' | 'cancelled'`.
**`error_kind` values:** `provider_5xx | provider_timeout | provider_rate_limit | content_policy | malformed_output | cancelled | orphaned | quota_exceeded | bad_request`.

**Retention:** no policy in v1. Orphan reaper keeps the `running` set clean (see §8.6).

**Not persisted:** prompt text, generated cards, PDF bytes. Only row counts + `metadata` fields + optional `error` message survive.

### 5.2 `ai_quota_daily` — already complete

No schema change. Columns already present: `user_id, day, prompt_calls, pdf_calls, pdf_pages, check_calls, plan_calls, cross_subject_rank_calls, quiz_calls, quiz_demo_used, extract_keywords_calls, cents_spent`. Column name is `day` (not `date`).

Starting limits (tune post-launch), encoded in `DefaultQuotaLimits()`:

```go
type QuotaLimits struct {
    PromptCalls int // 20 / day
    PDFCalls    int // 5  / day
    PDFPages    int // 100 / day (separate counter)
    CheckCalls  int // 50 / day
}
```

Loaded at boot; passed to `aipipeline.NewService`. Override via env vars if/when needed — not in v1.

### 5.3 Entitlement: no column — reuses `user_subscriptions`

Entitlement is `SELECT user_has_ai_access($1)` via `access.Service.HasAIAccess`. The SQL function (already in `db_sql/setup_billing.go`) returns true when the user has an `active` / `trialing` / `comp` row whose `current_period_end` (if set) is in the future. `POST /admin/grant-ai-access` inserts/updates a `comp` row for beta/dev; Spec C later writes `active` rows from Stripe webhooks through the same table.

### 5.4 New error sentinel

`internal/myErrors`:

```go
var ErrContentPolicy = errors.New("ai content policy refusal")
```

`internal/httpx.WriteError` maps it to HTTP 422. No other sentinel changes — `ErrNoAIAccess`, `ErrQuotaExhausted`, `ErrForbidden`, `ErrConflict`, `ErrPdfTooLarge`, `ErrAIProvider`, `ErrNotImplemented` all already exist.

### 5.5 No changes to existing tables

Subjects, chapters, flashcards, preferences, gamification — untouched. AI-generated FCs use the existing `flashcard-create` code path via `commit-generation`; `flashcards.source='ai'` is already a valid CHECK value. Chapters created by `commit-generation` use the existing `chapter-create` code path.

## 6. API Surface

All endpoints live under the existing server. All require `Auth` + `RequireVerified` middleware unless noted. Entitlement + quota are enforced inside the pipeline.

### 6.1 `POST /ai/flashcards/prompt` — SSE

**Content-Type:** `application/json`

```json
{
  "subject_id": 1,
  "chapter_id": 42,
  "prompt": "…≤ 8000 chars…",
  "target_count": 0,
  "style": "standard",
  "focus": ""
}
```

| Field | Type | Notes |
|-------|------|-------|
| `subject_id` | int | Required. Caller must have editor access (`access.SubjectLevel >= LevelEditor`). |
| `chapter_id` | int | Optional. If set, all generated cards are assigned to it; no `chapter` events emitted. |
| `prompt` | string | Required. Max 8000 chars. |
| `target_count` | int | 0 = auto, else 1–50. |
| `style` | string | `"short"` / `"standard"` / `"detailed"`. |
| `focus` | string | Optional free text, max 500 chars. |

**Response:** `text/event-stream` (via `httpx.Stream`).

```
event: job       data: {"jobId": 123}
event: card      data: {"chapterIndex":null,"title":"…","question":"…","answer":"…"}
event: done      data: {"itemsEmitted":24,"itemsDropped":1}
event: error     data: {"code":"provider_timeout","message":"…"}
```

Nothing is written to `chapters` or `flashcards` tables by this endpoint.

**Errors:** see §8.1.

### 6.2 `POST /ai/flashcards/pdf` — SSE

**Content-Type:** `multipart/form-data`

| Field | Type | Notes |
|-------|------|-------|
| `subject_id` | int | Required. Editor access. |
| `chapter_id` | int | Optional. Disables `auto_chapters`. |
| `file` | file | Required. Max 20 MB. Hard page cap: 30. |
| `coverage` | string | `"essentials"` / `"balanced"` / `"comprehensive"`. Default `"balanced"`. |
| `style` | string | `"short"` / `"standard"` / `"detailed"`. |
| `focus` | string | Optional, max 500 chars. |
| `auto_chapters` | bool | Only honored when `chapter_id` unset. |

**Response:** same `job` / `card` / `done` / `error` events, plus:

```
event: progress  data: {"phase":"analyzing","page":3,"total":12}
event: chapter   data: {"index":0,"title":"Derivatives"}
```

Cards carry `chapterIndex: int | null`, referencing the order `chapter` events arrived. When `auto_chapters=false`, no `chapter` events are emitted and cards carry `chapterIndex: null`.

### 6.3 `POST /ai/check` — JSON

```json
{ "flashcard_id": 42, "draft_question": "…", "draft_answer": "…" }
```

Both `draft_*` are optional. When present, they're what gets checked (so the user doesn't need to save-then-check). When absent, the server loads the stored FC.

**Response:**

```json
{
  "jobId": 124,
  "verdict": "ok",
  "findings": [
    {"kind": "factual", "text": "…"},
    {"kind": "style",   "text": "…"},
    {"kind": "typo",    "text": "…"}
  ],
  "suggestion": {
    "title":    "…",
    "question": "…",
    "answer":   "…"
  }
}
```

`suggestion` is always present even on `verdict: "ok"` (echoes input). Simpler client code than a nullable field. `verdict` is one of `"ok" | "minor_issues" | "major_issues"`.

No short-term (burst) rate limit — daily `check_calls` only.

### 6.4 `POST /ai/commit-generation` — JSON

Writes the user-edited draft to the DB atomically.

```json
{
  "job_id": 123,
  "subject_id": 1,
  "chapters": [
    {"clientId": "c1", "title": "Derivatives"},
    {"clientId": "c2", "title": "Integrals"}
  ],
  "cards": [
    {"chapterClientId": "c1", "title": "…", "question": "…", "answer": "…"},
    {"chapterClientId": null, "title": "…", "question": "…", "answer": "…"}
  ]
}
```

`clientId` is a frontend-generated string — lets the server map cards to chapters without real chapter IDs yet. All inserts happen in one transaction; `flashcards.source='ai'` for every card.

**Response:**

```json
{
  "subjectId": 1,
  "chapterIds": {"c1": 42, "c2": 43},
  "cardIds": [101, 102, 103]
}
```

### 6.5 `GET /ai/quota` — JSON

Cheap read for the quota badge. No provider call, no job row.

```json
{
  "aiAccess": true,
  "prompt": { "used": 4,  "limit": 20, "resetAt": "2026-04-24T00:00:00Z" },
  "pdf":    { "used": 1,  "limit": 5,  "pagesUsed": 14, "pagesLimit": 100, "resetAt": "2026-04-24T00:00:00Z" },
  "check":  { "used": 12, "limit": 50, "resetAt": "2026-04-24T00:00:00Z" }
}
```

`aiAccess` reflects `access.Service.HasAIAccess`. `resetAt` is midnight UTC — matches `ai_quota_daily.day` rollover.

### 6.6 `POST /admin/grant-ai-access` — JSON (admin)

Replaces old spec's `POST /admin/set-ai-subscription`. Wired under `Auth` + `RequireVerified` + new `RequireAdmin` middleware (checks `users.is_admin=true`). No `ADMIN_API_ENABLED` env flag.

**Request:**

```json
{ "user_id": 42, "active": true }
```

- `active=true` UPSERTs a `user_subscriptions` row with `plan='comp', status='comp'`. Leaves any existing Stripe `plan='pro_*'` row untouched.
- `active=false` marks any existing `comp` row as `status='canceled'`. Does not touch Stripe-originated rows.

**Response:**

```json
{ "userId": 42, "aiAccess": true }
```

(`aiAccess` reads `user_has_ai_access` post-mutation.)

**Errors:** `400` bad input, `403` not admin, `404` user not found.

## 7. Frontend Flows

Unchanged from the original §7 — generation entry points, `FlashcardGeneratePage.vue`, `FlashcardGenerateReviewPage.vue`, AI-check flow, paywall, store shape. Two mechanical corrections:

- Repo path: `/Users/martonroux/Documents/WEB/studbud_3/studbud/`.
- Endpoint renames: `/ai/flashcards/prompt` (JSON) and `/ai/flashcards/pdf` (multipart) — pick the client based on `mode`. `/ai/check` (not `/ai/check-flashcard`).
- Store field: `subscriptionActive` → `aiAccess` (matches backend).

Everything else — two-page flow (Generate → Review), streaming card accordion, chapter rename/merge, per-field Apply in the check modal, partial-result handling on stream drop — stays as originally specified.

### 7.1 Generation entry points

- **Subject detail** — "Generate with AI" button, visible to users with editor access. Subscription off → routes to paywall. Viewer-only → button hidden.
- **Chapter detail** — same button, prefills `chapter_id` and disables `auto_chapters`.

### 7.2 `FlashcardGeneratePage.vue`

Routes: `/subjects/:id/generate`, `/chapters/:id/generate`.

- **Header:** "Generate flashcards" + target context string.
- **Mode tabs:** `Prompt` / `PDF`.
- **Prompt tab:** textarea, max 8000 chars, live counter.
- **PDF tab:** drag-drop + file picker. Client-side validate ≤ 20 MB and ≤ 30 pages (via `pdf.js` when feasible); server re-checks.
- **Options block (`AiGenerationControls.vue`):**
  - Prompt mode: `target_count` slider (5–50, step 5) with "Auto" toggle (default auto).
  - PDF mode: `coverage` 3-tile segmented control (default `balanced`).
  - `style` 3-tile segmented control (Short / Standard / Detailed).
  - `focus` textarea (max 500 chars).
  - `auto_chapters` toggle; greyed with tooltip when `chapter_id` set or on Prompt tab.
- **Footer:** `QuotaBadge` on the left; Generate button on the right with cost callout. Disabled when quota exhausted.
- **On submit:** store params + file reference in `aiStore`; navigate to review page; review page kicks off the SSE request (so router back-nav doesn't orphan the stream).

### 7.3 `FlashcardGenerateReviewPage.vue`

Route: `/subjects/:id/generate/review`.

**Top bar:**
- Back button → confirm dialog ("Discard N generated cards?") if anything was streamed; else plain back nav.
- Title.
- Status pill: `Generating… (7/∞)` / `Generated 24 cards` / `Stopped at 12 cards` / `Cancelled at 5 cards`.

**While streaming:**
- Progress widget (phase, page counter) + "Cancel generation" button.
- As `event: card` arrives, a new collapsed row animates in. Accordion-style: tap to expand in place, one at a time. Expanded row replaces its preview with the full markdown editor (reuses `MarkdownToolbar` + `MarkdownPreview`) + title input + chapter dropdown + delete.
- If `auto_chapters` produced `chapter` events, the list is grouped by chapter sections. Chapter headers are editable (rename) and support "Merge with previous." An "Add chapter" button sits above the list.
- Cards can be reassigned between chapters via the row's chapter dropdown.

**After stream termination:**
- Progress widget replaced by a banner: success count + error kind (if any).
- Sticky bottom **Commit** button: "Save N flashcards" (+ "and M chapters" if any). Disabled if `N=0`.
- **Discard** secondary button (confirm dialog).

**Commit:**
- Calls `POST /ai/commit-generation`, shows spinner.
- Success → toast + navigate to subject detail.
- Failure → toast + stay on review page for retry.

**Stream interruption:** SSE drops (no `error`, no `done`) → status flips to "Stopped at N cards." Cards remain editable. Explicit `error` event → same behavior with error-code-specific banner. User cancels → client aborts `fetch()`, backend sees `ctx.Cancel()`, job finalized `cancelled`. Router `beforeLeave` guard: confirm dialog if cards exist.

### 7.4 AI-check flow

**Entry:** "Check with AI" button in the flashcard editor (both create and edit variants).

- Disabled when `title` AND `question` are both empty.
- Subscription off → routes to paywall.
- Quota exhausted → disabled + badge; tooltip shows reset time.

**`AiCheckModal.vue` — three states:**

- **Loading:** centered spinner + "Checking this flashcard…" + Cancel button (aborts `fetch`, closes modal).
- **Result:**
  - Verdict banner (green / amber / red) keyed on `verdict`.
  - Findings list with type tag + body (collapsed to placeholder when empty).
  - Diff view over title / question / answer (side-by-side desktop, stacked mobile).
  - Per-field Apply buttons + "Apply all." Applied fields show "Applied ✓" and fade.
- **Error:** code-specific copy + Retry button where retry makes sense.

**Apply behavior:** writes the suggested field value into the `flashcardDraft` store (editor's live draft). User must still hit Save to persist. Closing the modal keeps applied changes; dismissing without applying discards nothing.

### 7.5 Paywall

`PaywallCard.vue` — single CTA "Subscribe" showing "Coming soon" in v1. Shown when `aiAccess === false` on any AI entry-point tap.

### 7.6 Store — `stores/ai.ts`

```ts
interface AiGenerationJob {
  status: 'idle' | 'streaming' | 'complete' | 'failed' | 'cancelled'
  jobId: number | null
  subjectId: number
  chapterId: number | null
  params: GenerateParams
  progress: { phase: string; page?: number; total?: number } | null
  chapters: Array<{ clientId: string; title: string; order: number }>
  cards: Array<{
    clientId: string
    chapterClientId: string | null
    title: string
    question: string
    answer: string
    edited: boolean
  }>
  error: { code: string; message: string } | null
}

interface AiCheckState {
  open: boolean
  running: boolean
  flashcardId: number | null
  result: {
    verdict: 'ok' | 'minor_issues' | 'major_issues'
    findings: Array<{ kind: 'factual' | 'style' | 'typo'; text: string }>
    suggestion: { title: string; question: string; answer: string }
  } | null
  applied: { title: boolean; question: boolean; answer: boolean }
  error: { code: string; message: string } | null
}
```

Plus a cached `quota` snapshot, invalidated on every successful `commit-generation` and `check-flashcard`.

## 8. Error Handling, Observability, Safety

### 8.1 Error taxonomy

Every backend error surfaces as the skeleton's envelope: `{"error":{"code":"...","message":"...","field":null}}`. Frontend switches on `error.code`, never on `status` or `message`.

| `code` | Sentinel | HTTP | UI behavior |
|---|---|---|---|
| `no_ai_access` | `ErrNoAIAccess` | 402 | Route to paywall |
| `quota_exceeded` | `ErrQuotaExhausted` | 429 | Banner + disable button until `resetAt` |
| `forbidden` | `ErrForbidden` | 403 | "You don't have edit access to this subject" |
| `bad_request` | `ErrValidation` / `ErrInvalidInput` | 400 | Inline form error |
| `pdf_too_large` | `ErrPdfTooLarge` | 429 | "PDF must be under 20 MB and 30 pages" |
| `pdf_unreadable` | `ErrValidation` + code | 400 | "Couldn't read this PDF. Try exporting again" |
| `content_policy` | `ErrContentPolicy` | 422 | "The model declined to process this content." |
| `provider_timeout` | `ErrAIProvider` + code | 502 | "AI service timed out." + Retry |
| `provider_5xx` | `ErrAIProvider` + code | 502 | Same |
| `provider_rate_limit` | `ErrAIProvider` + code | 502 | "AI service is busy. Try again in a minute." |
| `malformed_output` | `ErrAIProvider` + code | 502 | "Couldn't parse the AI's response." + Retry |
| `concurrent_generation` | `ErrConflict` | 409 | "A generation is already running." + resume link with `jobId` |
| `cancelled` | (SSE-only; no HTTP response) | — | Silent — user initiated |
| `internal` | (default) | 500 | Generic "Something went wrong" |

### 8.2 Partial-result contract

For `/ai/flashcards/prompt` and `/ai/flashcards/pdf`, the SSE terminal event is always exactly one of: `done`, `error`, or connection close. Cards emitted before termination are kept; the user can commit them regardless. Quota is debited as described in §6 (pages upfront; calls on success only).

### 8.3 Short-term rate limits

- **Concurrent generations:** max 1 running generation per user across `generate_prompt` + `generate_pdf`. Enforced inside the pipeline via `ai_jobs WHERE status='running'`. Second concurrent → `concurrent_generation` (HTTP 409) with the existing `jobId` in `AppError.Message`.
- **Check-call short-term bucket: not implemented in v1.** Daily `check_calls` only.
- No global rate limit in v1.

### 8.4 PDF safety rails

- Size/page caps enforced before the file hits disk (multipart reader with `MaxMemory` + total limit).
- PDF → image conversion in a bounded-concurrency worker pool inside `internal/aiProvider/` (caps CPU/memory even under quota-permitted concurrency).
- Per-page timeout; any page exceeding it aborts the job with `pdf_unreadable`.
- No PDF file retained after conversion; images are provider-call in-memory only.

### 8.5 Content-safety posture

- Input sanitization: strip null bytes, normalize unicode, enforce length caps. Markdown passes through.
- No client-side pre-flight check. Provider refusals surface after the fact as `content_policy`.
- Prompt content, output content, and PDF bytes are NEVER persisted. `ai_jobs.metadata` holds only the knobs (style, focus, coverage, target_count).
- Model output exists only in the HTTP response (and temporarily in frontend state) until the user explicitly commits it.

### 8.6 Observability

- **Structured log per pipeline terminal state:** `job_id`, `user_id`, `feature_key`, `status`, `input_tokens`, `output_tokens`, `cents_spent`, `duration_ms`, `error_kind`. One line per job close. Routes through the existing `Logger` middleware pattern.
- **Metrics:** none in v1. `ai_jobs` + `ai_quota_daily` SQL queries serve as the dashboard.
- **Cron job — `aiJobsOrphanReaper`:** every 10 minutes, `UPDATE ai_jobs SET status='failed', error_kind='orphaned' WHERE status='running' AND started_at < now() - interval '1 hour'`. Registered in `internal/cron/` alongside the skeleton's existing `ai_quota_daily` GC (rows older than 30 days at 04:00 UTC) and `ai_extraction_jobs` stale-claim reclaim.

## 9. Testing Strategy

### 9.1 Backend

Real Postgres. No mocks. Uses the existing `testutil/` package.

- **`pkg/aipipeline/service_test.go`** (real DB + `testutil.FakeAIClient`):
  - Entitlement denial (no `comp` / active sub row) → `ErrNoAIAccess`.
  - Quota exhaustion (pre-seeded `ai_quota_daily` row at limit) → `ErrQuotaExhausted`.
  - Happy path generation: streamed chunks → `ai_jobs.status='complete'`, correct `items_emitted`, `prompt_calls` incremented by 1.
  - Schema-violating item mid-stream → dropped + `items_dropped` incremented + job still completes.
  - Provider 5xx once → transparent retry → success.
  - Provider 5xx twice → `status='failed'`, `error_kind='provider_5xx'`, partial items preserved.
  - Content-policy refusal → `status='failed'`, `error_kind='content_policy'`, no retry.
  - Malformed output → `status='failed'`, `error_kind='malformed_output'`, no retry.
  - `ctx.Cancel()` → `status='cancelled'`, job finalized.
  - Concurrent-generation cap → second call returns `ErrConflict` with the first `jobId`.
  - PDF flow: `pdf_pages` debited upfront; if debit would exceed, quota error fires before provider call.
- **`pkg/aipipeline/quota_test.go`** (real DB):
  - Per-feature counter independence.
  - Day rollover (clock fixture).
  - UPSERT concurrency via `ON CONFLICT DO UPDATE`.
- **`api/handler/ai_test.go`** (`httptest.NewServer(routes.Register(deps))`):
  - SSE event ordering: `job → (card|chapter|progress)* → done`.
  - `commit-generation` transaction: chapters + cards in one tx; forced failure rolls back both.
  - `quota` reflects recent debits.
  - `admin/grant-ai-access` gated by `RequireAdmin`: non-admin gets 403; admin insertion makes `aiAccess` flip to true on the next `quota` read.
- **`testutil/ai.go`** already exists; extend with helpers:
  - `SeedAIAccess(t, db, uid)` — inserts a `comp` `user_subscriptions` row.
  - `SeedQuotaAt(t, db, uid, feature, value)` — pre-loads `ai_quota_daily`.
  - `FakeAIClient.QueueChunks(...)` — lets tests script streams.
- **Excluded:** real Anthropic calls (fake only in CI); `go-fitz` internals.

### 9.2 Frontend

No test framework in place; Vitest / Playwright adoption is not part of Spec A. Manual QA checklist:

1. Editor-access subject: "Generate with AI" visible; viewer-only: hidden; subscription-off: routes to paywall.
2. Prompt generation: cards stream in, edit one inline, delete one, commit → subject reflects final set.
3. PDF generation with `auto_chapters`: chapters appear, rename one, reassign a card, commit → DB state matches edited state.
4. Cancel mid-stream: partial cards retained; commit works on subset.
5. Back-nav mid-stream: confirm dialog fires; clean state after confirm.
6. Quota badge updates after commit.
7. AI-check modal: per-field Apply writes draft; editor Save persists. Dismiss applies nothing.
8. Quota exhausted: Generate and Check disabled with tooltip.
9. Content-policy rejection: banner text is human-readable.
10. SSE drop mid-stream: "Stopped at N cards," cards remain editable.

### 9.3 Prompt quality

Not automatable in v1. Pre-merge ritual: run 5 real prompts + 3 real PDFs, eyeball outputs, save reference prompts+outputs under `docs/superpowers/ai-evals/`. `cents_spent` logged in `ai_jobs` from day one; first-week post-mortem informs quota tuning.

## 10. Out of Scope (Deferred to Later Specs)

| Item | Destination |
|------|-------------|
| Real billing (Stripe + Apple IAP + Google Play IAP + webhooks + tax + refunds) | Spec C |
| AI revision planning (exam calendar, depth, annales) | Spec B |
| Flashcard keyword index | Spec B.0 |
| Knowledge quiz generator | Spec D |
| AI Duel | Spec E |
| Save-time auto-check in the FC editor | Post-v1 enhancement |
| Async job UI for PDFs > 30 pages | Post-v1 enhancement |
| "My AI history" page | Post-v1 enhancement |
| Batch AI operations ("check whole chapter," "regenerate weak cards") | Separate spec |
| Automated prompt-quality eval harness | Later |
| Provider fallback chain / multi-provider | Post-v1 |
| Prometheus/Grafana dashboards | Operational upgrade, optional |
| Frontend test infrastructure (Vitest / Playwright) | Separate decision |
| FC versioning / history | Needed for hub features, not for Spec A |
| In-app "report bad AI result" flow | Later, after observability informs priority |

## 11. Non-Goals

- Spec A does not modify training, mastery, `dueHeuristic`, or gamification. AI-generated FCs enter as `last_result=-1` and flow through the existing system unchanged.
- Spec A does not migrate existing flashcards. `flashcards.source` defaults to `'manual'` for legacy rows; AI-generated flashcards created via `commit-generation` set `source='ai'`. No backfill.
- Spec A does not introduce marketing surfaces (onboarding, announcement modals, push notifications).

## 12. Spec A Dependencies

**New Go dependencies:**
- `github.com/gen2brain/go-fitz` — PDF → image. Only imported from `internal/aiProvider/`.
- No Anthropic SDK. `ClaudeProvider` uses the standard library's `net/http` against Anthropic's REST + SSE endpoints.

**No new DB deps.**

**Config fields (already present in `internal/config/config.go`):**
- `AnthropicAPIKey` — required in prod (existing validation).
- `AIModel` — default `"claude-sonnet-4-6"`.

**Schema changes:** only the additive `ALTER TABLE` in §5.1. All idempotent. No migration tool.

**New middleware:** `internal/http/middleware.RequireAdmin`. Reused by any future admin route.

## 13. Acceptance Criteria

Spec A is "shipped" when all of the following hold:

1. `go vet ./... && go build ./...` clean.
2. `make test` passes, including the new tests in §9.1.
3. Admin grants `comp` access: `POST /admin/grant-ai-access` → `GET /ai/quota` returns `aiAccess: true`.
4. `POST /ai/flashcards/prompt` streams ≥ 1 valid card for a real prompt against the live Anthropic API in staging; `POST /ai/commit-generation` persists them with `source='ai'`.
5. `POST /ai/flashcards/pdf` streams cards for a real PDF; `ai_jobs.pdf_page_count` and `ai_quota_daily.pdf_pages` update correctly.
6. `POST /ai/check` returns a verdict + findings + suggestion for a real flashcard.
7. Quota exhaustion returns `code=quota_exceeded` (HTTP 429) with a `resetAt` hint.
8. Concurrent second generation returns `code=concurrent_generation` (HTTP 409) with the first `jobId`.
9. Orphan reaper flips a hand-injected stale `running` row to `failed` + `error_kind='orphaned'` within 10 minutes.
10. Content-policy refusal surfaces as `code=content_policy` (HTTP 422).
11. Malformed model output surfaces as `code=malformed_output` (HTTP 502); no transparent retry fired.
