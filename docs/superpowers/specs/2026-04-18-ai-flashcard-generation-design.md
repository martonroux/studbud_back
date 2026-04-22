# Spec A — AI Flashcard Generation & AI Check

**Status:** Design approved, ready for implementation planning.
**Date:** 2026-04-18
**Scope:** Single spec. Does not cover AI revision planning (Spec B), real subscription billing (Spec C), or downstream AI features (quiz, duel).

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
| Entitlement model | Stub: boolean `ai_subscription_active` on users, flipped via a dev-only admin endpoint. | Real billing (Stripe + IAP) is Spec C. Unblocks Spec A + B beta now. |
| Cost control | Per-request hard limits + per-user daily quotas (separate counters for prompt, PDF, PDF-pages, check). No free tier. | PDF has stricter caps than prompt. Tune numbers post-launch. |
| PDF ingestion | Vision multimodal — PDF pages → images → Claude. No text extraction path. | Covers scanned polys, handwriting, diagrams, math uniformly. Subscription caps exposure. |
| Generation inputs | Target subject (+ optional chapter), prompt OR PDF, target count (or auto) for prompt / 3-level **coverage** for PDF, style, focus text, **chapter auto-creation** (PDF only). | Prompt mode lets the user pin a count; PDF mode instead picks how much of the source to cover (`essentials` / `balanced` / `comprehensive`) because card count depends on the PDF. |
| Preview flow | Generated cards land on a staging/review screen. User edits / deletes / reassigns / renames chapters / merges chapters, then "Save all" commits atomically. | Protects the user from hallucinations; no FC versioning exists yet. |
| AI-check output | Verdict (`ok` / `minor_issues` / `major_issues`) + typed findings (factual / style / typo) + a suggested rewrite. | Giving a rewrite is what makes the button actually useful. |
| AI-check UI | Modal with diff view over title / question / answer. Per-field Apply. Apply writes to editor's in-memory draft; user still clicks Save to persist. | Matches the editor's existing save model; keeps suggestions as proposals, not commits. |
| Streaming | Generation: Server-Sent Events. Check: non-streaming (small, fast, atomic). Async jobs for very large PDFs: later. | Streaming pays off for multi-card generation. Small single-shot responses gain nothing from SSE. |
| Error model | Partial results preserved on failure. One transparent backend retry on transient provider errors (5xx / timeout / 429). All other errors surface immediately with context. | Users don't lose completed work; one retry absorbs most transient flakiness. |
| Architecture | Single generic AI pipeline (`RunStructuredGeneration`) + thin per-feature handlers. | Entitlement, quota, retry, audit, SSE framing, schema validation all live in one place. Spec B reuses it unchanged. |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`study_buddy_backend/`):**

- `pkg/ai/` — provider-agnostic types: `AIMessage`, `AIChunk`, `FeatureKey` enum, prompt templates (`//go:embed` text files).
- `internal/aiProvider/` — concrete `ClaudeProvider` behind a `Provider` interface. Owns HTTP to Anthropic, SSE parsing of provider stream, retry backoff, PDF → image conversion (`go-fitz` or equivalent). Only module that knows about Anthropic.
- `api/service/aiService.go` — the `RunStructuredGeneration` pipeline primitive. Every AI feature goes through it.
- `api/service/aiQuotaService.go` — per-user daily counters. UPSERT row per user per day in `ai_quota_daily`.
- `api/handler/aiHandler.go` — thin handlers: `generate-flashcards`, `commit-generation`, `check-flashcard`, `quota`.
- `pkg/aiJob/` — `ai_jobs` row model + repository.
- Admin: `api/handler/adminHandler.go` (new) — `POST /admin/set-ai-subscription`, guarded by `ADMIN_API_ENABLED=true` env var.

**Frontend (`studbud/src/`):**

- `api/ai.ts` — SSE-aware client for generation; plain `fetch` for check / quota.
- `stores/ai.ts` — Pinia store: current generation job (streamed draft cards + proposed chapters + progress + error), AI-check modal state, cached quota snapshot.
- `pages/FlashcardGeneratePage.vue` — input form (`/subjects/:id/generate`, `/chapters/:id/generate`).
- `pages/FlashcardGenerateReviewPage.vue` — staging/preview/commit screen (`/subjects/:id/generate/review`).
- `components/ai/AiGenerationControls.vue` — count / style / focus / difficulty-distribution slider / auto-chapters toggle.
- `components/ai/AiCheckModal.vue` — diff + per-field Apply.
- `components/ai/QuotaBadge.vue` — remaining quota indicator.
- `components/ai/PaywallCard.vue` — placeholder shown when entitlement is off.
- Entry points: "Generate with AI" button on subject-detail and chapter-detail; "Check with AI" button in the flashcard editor.

### 3.2 Hard boundaries

- Frontend NEVER talks directly to the provider.
- `RunStructuredGeneration` is the ONLY function that invokes `aiProvider`. Any new AI feature must go through it.
- Entitlement + quota checks are enforced inside the pipeline, never in handlers. Adding an AI endpoint that forgets the check is structurally impossible.
- Prompts live in versioned template files under `pkg/ai/prompts/`, not inline strings in handlers.

## 4. The AI Pipeline Primitive

Located in `api/service/aiService.go`. Handlers pass messages + a JSON schema + a feature key; the pipeline returns a channel of validated, typed chunks.

```go
type FeatureKey string

const (
    FeatureGenerateFromPrompt FeatureKey = "generate_prompt"
    FeatureGenerateFromPDF    FeatureKey = "generate_pdf"
    FeatureCheckFlashcard     FeatureKey = "check_flashcard"
)

type AIRequest struct {
    UserID    string
    Feature   FeatureKey
    Messages  []AIMessage          // role + parts (text or image)
    Schema    JSONSchema           // expected item shape
    MaxTokens int
    PDFPages  int                  // 0 for non-PDF features
}

type AIChunk struct {
    Kind     ChunkKind             // "item" | "progress" | "done" | "error"
    Item     json.RawMessage       // one schema-validated item (for streamed arrays)
    Progress *ProgressInfo         // optional ("analyzing page 3/12", etc.)
    Err      *AIError
}

type AIError struct {
    Kind    string                 // see Error Taxonomy table
    Message string                 // safe-to-display
}

func (s *AIService) RunStructuredGeneration(ctx context.Context, req AIRequest) (<-chan AIChunk, jobID int64, error)
```

### 4.1 Pipeline steps, in order

1. **Entitlement check.** Reject `not_entitled` if `users.ai_subscription_active == false`.
2. **Quota check.** Read today's `ai_quota_daily` row. Reject `quota_exceeded` with `ResetAt` when the relevant counter is at the per-feature limit.
3. **Job row insert.** `ai_jobs` row, `status='running'`, request params + input hash + PDF page count. Return the job ID to the handler.
4. **Provider stream start.** Call `aiProvider.Stream(ctx, …)`. On transient failure (provider 5xx / timeout / 429), one transparent retry with short backoff. No retry on 4xx or content policy.
5. **Incremental JSON validation.** Streaming parser consumes provider tokens; as each complete item (e.g. one FC object in a streamed array) closes, validate it against `Schema`. Valid → emit `AIChunk{Kind:"item"}`. Invalid → drop with logged warning + increment `items_dropped`. Don't abort the stream.
6. **Quota debit per accepted item.** Increment counters only as items succeed, not upfront.
7. **Terminal event + job finalization.** On stream close or error, update the job row (`status`, `finished_at`, token counts, cost estimate, error kind). Always — including on `ctx.Cancel()`.

### 4.2 What the pipeline does NOT do

- Does not know what a flashcard is. Schema + messages come from handlers.
- Does not know SSE framing. Handler adapts the `<-chan AIChunk` to the HTTP transport.
- Does not retry beyond one transparent attempt. User re-triggers from the UI for anything else.
- Does not persist prompt content, output content, or uploaded PDF bytes.

### 4.3 Provider abstraction

`internal/aiProvider.Provider` is a narrow interface:

```go
type Provider interface {
    Stream(ctx context.Context, msgs []AIMessage, schema JSONSchema, maxTokens int) (<-chan providerEvent, error)
}
```

One concrete implementation (`ClaudeProvider`). PDF → image conversion lives here (vision-only, no text fallback in v1). Bounded-concurrency worker pool for PDF conversion. Per-page timeout on conversion.

## 5. Data Model Changes

### 5.1 `users` table — new column

```sql
ALTER TABLE users ADD COLUMN ai_subscription_active BOOLEAN NOT NULL DEFAULT FALSE;
```

Stub entitlement flag. Flipped by `POST /admin/set-ai-subscription` (dev-only). Checked inside `RunStructuredGeneration` before any provider call. Real billing in Spec C will replace/override the write path but not the column.

### 5.2 `ai_jobs` — new table

Durable audit + resumability record. One row per `RunStructuredGeneration` invocation.

```sql
CREATE TABLE ai_jobs (
    id               BIGSERIAL PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id),
    feature          TEXT NOT NULL,
    status           TEXT NOT NULL,             -- running | complete | failed | cancelled
    subject_id       BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    flashcard_id     BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL,
    request_params   JSONB NOT NULL,
    input_hash       TEXT NOT NULL,             -- sha256(prompt) or sha256(PDF bytes)
    pdf_page_count   INTEGER NOT NULL DEFAULT 0,
    items_emitted    INTEGER NOT NULL DEFAULT 0,
    items_dropped    INTEGER NOT NULL DEFAULT 0,
    provider         TEXT NOT NULL,
    provider_req_id  TEXT NULL,
    input_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens    INTEGER NOT NULL DEFAULT 0,
    cost_cents       INTEGER NOT NULL DEFAULT 0,
    error_kind       TEXT NULL,
    error_message    TEXT NULL,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at      TIMESTAMPTZ NULL
);
CREATE INDEX ai_jobs_user_started_idx ON ai_jobs (user_id, started_at DESC);
CREATE INDEX ai_jobs_running_idx ON ai_jobs (status) WHERE status = 'running';
```

**Not persisted:** prompt text, generated cards, PDF bytes. Only the hash + page count survive.

**No retention policy in v1** — table grows unbounded; revisit when size matters.

**Orphan reaper:** a periodic job marks `status='running'` rows older than one hour as `failed` with `error_kind='orphaned'`. Handles crash-without-finalization. Correctness, not storage.

### 5.3 `ai_quota_daily` — new table

Per-user-per-day counters. UPSERT on write.

```sql
CREATE TABLE ai_quota_daily (
    user_id       TEXT NOT NULL REFERENCES users(id),
    date          DATE NOT NULL,                 -- user-local-day, same convention as daily_goal
    prompt_calls  INTEGER NOT NULL DEFAULT 0,
    pdf_calls     INTEGER NOT NULL DEFAULT 0,
    pdf_pages     INTEGER NOT NULL DEFAULT 0,
    check_calls   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, date)
);
```

**Starting limits (tune post-launch):** 20 prompt generations / day; 5 PDF generations / day capped at 100 pages / day total; 50 check calls / day.

### 5.4 No changes to existing tables

Subjects, chapters, flashcards, preferences, gamification — untouched. AI-generated FCs use the existing `create-flashcard` path via the new `commit-generation` handler (transactional bulk insert).

## 6. API Surface

All endpoints live under the existing server. All require `auth` + `RequireVerified`. All relevant endpoints enforce `ai_subscription_active` inside the pipeline.

### 6.1 `POST /ai/generate-flashcards` — SSE

**Content-Type:** `multipart/form-data`

| Field | Type | Notes |
|-------|------|-------|
| `subject_id` | int | Required. User needs editor access. |
| `chapter_id` | int | Optional. If set, all generated cards are assigned to it; `auto_chapters` is ignored. |
| `mode` | string | `"prompt"` or `"pdf"`. |
| `prompt` | string | Required when `mode=prompt`. Max 8000 chars. |
| `file` | file | Required when `mode=pdf`. Max 20 MB. Hard page cap: 30. |
| `target_count` | int | Prompt mode only. 0 = auto, else 1–50. Ignored when `mode=pdf`. |
| `coverage` | string | PDF mode only. `"essentials"` / `"balanced"` / `"comprehensive"`. Required when `mode=pdf`. Default `"balanced"`. |
| `style` | string | `"short"` / `"standard"` / `"detailed"`. |
| `focus` | string | Optional free text, max 500 chars. |
| `auto_chapters` | bool | Only considered when `chapter_id` unset AND `mode=pdf`. |

**Response:** `text/event-stream`.

```
event: job       data: {"jobId": 123}
event: progress  data: {"phase":"analyzing","page":3,"total":12}
event: chapter   data: {"index":0,"title":"Derivatives"}
event: card      data: {"chapterIndex":0,"title":"…","question":"…","answer":"…","difficulty":1}
event: error     data: {"kind":"content_policy","message":"…"}   # terminal on failure
event: done      data: {"itemsEmitted":24,"itemsDropped":1}       # terminal on success
```

`chapterIndex` references the order `chapter` events arrived; when `auto_chapters=false`, no `chapter` events are emitted and cards carry `chapterIndex: null`. **Nothing is written to chapters/flashcards tables by this endpoint.**

**Errors:** `400`, `401`, `403` (unverified, `not_entitled`, `access_denied`), `404`, `409` (concurrent-generation cap), `413`, `429`, `500`, `502`, `504`.

### 6.2 `POST /ai/commit-generation` — JSON

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

`clientId` is a frontend-generated string — lets the server map cards to chapters without real chapter IDs yet. All creates happen in one transaction.

**Response:**

```json
{
  "subjectId": 1,
  "chapterIds": {"c1": 42, "c2": 43},
  "cardIds": [101, 102, 103]
}
```

**Errors:** `400`, `401`, `403` (editor access required), `404`, `500`.

### 6.3 `POST /ai/check-flashcard` — JSON

**Request:**

```json
{
  "flashcard_id": 42,
  "draft_question": "…",
  "draft_answer": "…"
}
```

Both `draft_*` are optional. When present, they're what gets checked (so the user doesn't need to save-then-check). When absent, the server loads the stored FC.

**Response:**

```json
{
  "jobId": 124,
  "verdict": "ok" | "minor_issues" | "major_issues",
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

`suggestion` is always present even on `verdict: "ok"` (echoes input). Simpler client code than a nullable field.

**Errors:** `400`, `401`, `403`, `404`, `422` (content policy), `429`, `502`, `504`.

### 6.4 `GET /ai/quota` — JSON

Cheap read for the quota badge. No provider call, no job row.

```json
{
  "subscriptionActive": true,
  "prompt": { "used": 4,  "limit": 20, "resetAt": "2026-04-19T00:00:00Z" },
  "pdf":    { "used": 1,  "limit": 5,  "pagesUsed": 14, "pagesLimit": 100, "resetAt": "2026-04-19T00:00:00Z" },
  "check":  { "used": 12, "limit": 50, "resetAt": "2026-04-19T00:00:00Z" }
}
```

### 6.5 `POST /admin/set-ai-subscription` — dev-only

Guarded by env var `ADMIN_API_ENABLED=true`. Never registered when env var is unset. Body: `{"user_id": "...", "active": true}`. Lets operators grant access to beta users until Spec C lands.

## 7. Frontend Flows

### 7.1 Generation entry points

- **Subject detail** — "Generate with AI" button, visible to users with editor access. Subscription off → routes to paywall. Viewer-only → button hidden.
- **Chapter detail** — same button, prefills `chapter_id` and disables `auto_chapters`.

### 7.2 `FlashcardGeneratePage.vue`

Routes: `/subjects/:id/generate`, `/chapters/:id/generate`.

- **Header:** "Generate flashcards" + target context string.
- **Mode tabs:** `Prompt` / `PDF`.
- **Prompt tab:** textarea, max 8000 chars, live counter.
- **PDF tab:** drag-drop + file picker. Client-side validate ≤20 MB and ≤30 pages (via `pdf.js` when feasible); server re-checks.
- **Options block (`AiGenerationControls.vue`):**
  - Prompt mode: `target_count` slider (5–100, step 5) with "Auto" toggle (default auto).
  - PDF mode: `coverage` 3-tile segmented control — `Essentials` / `Balanced` / `Comprehensive` (default `balanced`). The exact card count is chosen by the model from PDF content; the user trims excess on the review screen.
  - `style` 3-tile segmented control (Short / Standard / Detailed).
  - `focus` textarea (max 500 chars).
  - `auto_chapters` toggle; greyed with tooltip when `chapter_id` set or on Prompt tab.
- **Footer:** `QuotaBadge` on the left; Generate button on the right with cost callout ("uses 1 of 20 today" or "uses 14 pages of 100 today"). Disabled when quota exhausted, with explanatory text.
- **On submit:** store params + file reference in `aiStore`; navigate to review page; review page kicks off the SSE request (so router back-nav doesn't orphan the stream).

### 7.3 `FlashcardGenerateReviewPage.vue`

Route: `/subjects/:id/generate/review`.

**Top bar:**
- Back button → confirm dialog ("Discard N generated cards?") if anything was streamed; else plain back nav.
- Title.
- Status pill: `Generating… (7/∞)` / `Generated 24 cards` / `Stopped at 12 cards` / `Cancelled at 5 cards`.

**While streaming:**
- Progress widget (phase, page counter) + "Cancel generation" button.
- As `event: card` arrives, a new **collapsed row** animates in. Accordion-style: tap to expand in place, one at a time. Expanded row replaces its preview with the full markdown editor (reuses `MarkdownToolbar` + `MarkdownPreview`) + title input + chapter dropdown + delete.
- If `auto_chapters` produced `chapter` events, the list is grouped by chapter sections. Chapter headers are editable (rename) and support "Merge with previous." An "Add chapter" button sits above the list.
- Cards can be reassigned between chapters via the row's chapter dropdown. Drag-between-chapters is a polish goal; not load-bearing.
- Optional polish: swipe-sideways on a collapsed row reveals delete + reassign-chapter actions (mobile).

**After stream termination:**
- Progress widget replaced by a banner: success count + error kind (if any).
- Sticky bottom **Commit** button: "Save N flashcards" (+ "and M chapters" if any). Disabled if `N=0`.
- **Discard** secondary button (confirm dialog).

**Commit:**
- Calls `POST /ai/commit-generation`, shows spinner on the button.
- Success → toast + navigate to subject detail.
- Failure → toast + stay on review page for retry.

**Stream interruption:**
- SSE connection drops (no `error`, no `done`): status flips to "Stopped at N cards." Cards already in memory remain editable.
- Explicit `error` event: same behavior, with error-kind-specific banner.
- User cancels: client aborts `fetch()`, backend sees `ctx.Cancel()`, job row finalized `cancelled`. User edits + commits what they have, or discards.
- Router `beforeLeave` guard: confirm dialog if cards exist.

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
  - **Diff view** over title / question / answer. Desktop: side-by-side. Mobile: stacked "Current / Suggested." Strikethrough for removed, tinted for added.
  - **Per-field Apply buttons:** "Apply title" / "Apply question" / "Apply answer" — each independent. Plus "Apply all." Applied fields show "Applied ✓" and fade.
- **Error:** kind-specific copy (`content_policy`, `provider_timeout`, `quota_exceeded`, etc.) + Retry button where retry makes sense.

**Apply behavior:** writes the suggested field value into the `flashcardDraft` store (editor's live draft). User must still hit the editor's Save button to persist. Closing the modal keeps applied changes; dismissing without applying discards nothing.

### 7.5 Paywall

`PaywallCard.vue` — single CTA "Subscribe" showing "Coming soon" in v1. Shown when `subscriptionActive === false` on any AI entry-point tap.

### 7.6 Store — `stores/ai.ts`

Single Pinia store covering both flows so pages can hand off state.

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
  error: { kind: string; message: string } | null
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
  error: { kind: string; message: string } | null
}
```

Plus a cached `quota` snapshot, invalidated on every successful `commit-generation` and `check-flashcard`.

## 8. Error Handling, Observability, Safety

### 8.1 Error taxonomy

Every backend error carries a stable `kind`. Frontend switches on `kind`, never on `message`.

| Kind | HTTP | UI behavior |
|------|------|-------------|
| `not_entitled` | 403 | Route to paywall |
| `quota_exceeded` | 429 | Banner + disable button until `resetAt` |
| `access_denied` | 403 | "You don't have edit access to this subject" |
| `bad_request` | 400 | Inline form error |
| `pdf_too_large` | 413 | "PDF must be under 20 MB and 30 pages" |
| `pdf_unreadable` | 400 | "Couldn't read this PDF. Try exporting again" |
| `content_policy` | 422 | "The model declined to process this content." |
| `provider_timeout` | 504 | "AI service timed out." + Retry |
| `provider_5xx` | 502 | Same |
| `provider_rate_limit` | 429 | "AI service is busy. Try again in a minute." |
| `malformed_output` | 502 | "Couldn't parse the AI's response." + Retry |
| `cancelled` | (SSE event only) | Silent — user initiated |
| `internal` | 500 | Generic "Something went wrong." |

### 8.2 Partial-result contract

For `/ai/generate-flashcards`, the stream's terminal event is always exactly one of: `done`, `error`, or connection close. Cards emitted before termination are kept; the user can commit them regardless of how the stream ended. Quota is debited per accepted card, not upfront.

### 8.3 Short-term rate limits (beyond daily quota)

- **Concurrent generations:** max 1 running job per user for `/ai/generate-flashcards`. Second request while one runs → `409 Conflict` with the existing `jobId` (frontend can resume or dedupe).
- **Check call spam guard:** max 2 calls per 10 seconds per user (in-memory token bucket).
- No global rate limit in v1.

### 8.4 PDF safety rails

- Size/page caps enforced before file hits disk (multipart reader with `MaxMemory` + total limit).
- PDF → image conversion runs in a bounded-concurrency worker pool (caps CPU/memory even under quota-permitted concurrency).
- Per-page timeout; any page exceeding it aborts the job with `pdf_unreadable`.
- No PDF file is retained after conversion; images sent to the provider are in-memory only.

### 8.5 Content-safety posture

- Input sanitization: strip null bytes, normalize unicode, enforce length caps. Markdown passes through as-is.
- No client-side pre-flight check. Provider refusals surface as `content_policy` after the fact.
- Prompt content, output content, and PDF bytes are NEVER persisted. `input_hash` in `ai_jobs` is the only durable trace.
- Model output exists only in the HTTP response (and temporarily in frontend state) unless the user explicitly commits it.

### 8.6 Observability

- **Structured logs** per pipeline call: `job_id`, `user_id`, `feature`, `status`, tokens, duration, `error_kind`. One line per terminal state.
- **Metrics (optional for v1):** counters for `ai_job_total{feature,status}`, histograms for `ai_job_duration_seconds{feature}`, counters for `ai_tokens_total` and `ai_cost_cents_total`. DB queries against `ai_jobs` are sufficient in v1.

## 9. Testing Strategy

### 9.1 Backend

- **Unit tests (`aiService_test.go`)** — with a `fakeAIProvider` double. Cover: entitlement denial, quota exhaustion, happy path, schema-violating item dropped mid-stream, provider 5xx with transparent retry success, provider 5xx twice → failed + partial preserved, content-policy refusal (no retry), context cancellation, concurrent-generation cap.
- **Unit tests (`aiQuotaService_test.go`)** — per-feature counter independence, day rollover matching `dailyGoal` conventions, UPSERT concurrency.
- **HTTP integration tests (`handlers_test.go`)** — thin wiring tests per endpoint: SSE event ordering, `commit-generation` transactionality + rollback on failure, `quota` reflects recent debits, admin endpoint gated by env var.
- **Excluded:** real provider calls (fake only); third-party PDF library internals.

### 9.2 Frontend

No test framework in place; Vitest / Playwright adoption is not part of Spec A. Manual QA checklist (runs before merge):

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

Not automatable in v1. Pre-merge ritual: run 5 real prompts + 3 real PDFs, eyeball outputs, save reference prompts+outputs under `docs/superpowers/ai-evals/`. Cost per call is logged in `ai_jobs` from day one; first-week post-mortem informs quota tuning.

## 10. Out of Scope (Deferred to Later Specs)

| Item | Destination |
|------|-------------|
| Real billing (Stripe + Apple IAP + Google Play IAP + webhooks + tax + refunds) | Spec C |
| AI revision planning (exam calendar, depth, annales) | Spec B |
| Knowledge quiz generator | Folded into Spec B |
| AI Duel | Later spec |
| Save-time auto-check in the FC editor | Post-v1 enhancement |
| Async job UI for PDFs > 30 pages | Post-v1 enhancement |
| "My AI history" page | Post-v1 enhancement |
| Batch AI operations ("check whole chapter," "regenerate weak cards") | Separate spec |
| Automated prompt-quality eval harness | Spec D territory |
| Provider fallback chain / multi-provider | Post-v1 |
| Prometheus/Grafana dashboards | Operational upgrade, optional |
| Frontend test infrastructure (Vitest / Playwright) | Separate decision |
| FC versioning / history | Needed for hub features, not for Spec A |
| In-app "report bad AI result" flow | Later, after observability informs priority |

## 11. Non-Goals

- Spec A does not modify training, mastery, `dueHeuristic`, or gamification. AI-generated FCs enter as `lastResult = -1` and flow through the existing system unchanged.
- Spec A does not add an "AI-generated" marker to flashcards. If desired later, it's a one-column addition.
- Spec A does not introduce marketing surfaces (onboarding, announcement modals, push notifications).

## 12. Spec A Dependencies (to be added in this work)

- `ai_subscription_active` column on `users`.
- `ai_jobs` table.
- `ai_quota_daily` table.
- `ANTHROPIC_API_KEY` backend config.
- PDF → image Go dependency (e.g. `go-fitz`).
- `ADMIN_API_ENABLED` env var for the dev-only admin endpoint.
