# Spec B.0 — Flashcard Keyword Index

**Status:** Design approved, ready for implementation planning.
**Date:** 2026-04-19
**Scope:** Backend-only. Builds the keyword-extraction subsystem that Spec B (AI Revision Plan) depends on for cross-subject flashcard discovery. No new HTTP endpoints. No user-visible UI.

---

## 1. Purpose

Maintain a lightweight, AI-generated keyword index over every flashcard so downstream AI features (Spec B revision plans, future search/related-cards features) can shortlist relevant cards cheaply — without re-reading full question/answer content through an LLM each time.

Each flashcard is reduced to 5–12 weighted keywords by a background worker. This index is pure derived data: it can be rebuilt from scratch at any time, and its only consumer today is the revision-plan cross-subject shortlist query.

## 2. Product Decisions (Locked)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Trigger | Async extraction on flashcard create + on update (if Q or A materially changed). | Keywords are cheap to generate but shouldn't block user writes. |
| Queue dedup | Per-FC: only one pending/running job per flashcard at a time. New enqueues coalesce onto the existing row (priority updated, `updated_at` bumped). | Spam-editing a card must not multiply AI cost. |
| Material-change filter | Skip re-extraction when combined Q+A byte-delta <20 chars AND Levenshtein ratio <0.1. | Typo fixes / trailing whitespace shouldn't burn quota. |
| Entitlement | All users. No per-user quota. Gated only by a system-wide token-bucket rate cap. | User prioritized breadth of feature reach; cost is system-side and absorbed. Can switch to AI-subscribers-only later if spend demands. |
| Backfill | Migration-time one-shot: all existing FCs enqueued at priority -1 when this ships. | App is pre-launch; existing rows are dev seed data. Batch enqueue is simpler than feature-flag rollout. |
| Public surface | None. No HTTP endpoints. Keywords are read only by internal queries (Spec B). | Minimizes API surface; internal plumbing only. |
| Queue infrastructure | DB-backed `ai_extraction_jobs` table + goroutine worker pool. | Zero new infra (no Redis/RabbitMQ). Transactional enqueue with the flashcard write. Observable through SQL. |
| Storage shape | Normalized `flashcard_keywords(fc_id, keyword, weight)` with index on `keyword`. | Makes Spec B's "find cross-subject FCs sharing K+ keywords, ranked by weight" a trivial join. |
| AI pipeline | Reuses Spec A's `aiService.RunStructuredGeneration` with a new `FeatureExtractKeywords` feature key. | No duplicate orchestration. Same retry/schema/validation path. |
| Cost accounting | System-side. Does not debit any user-facing quota counter. | Invisible background work; users never opt in. |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`study_buddy_backend/`):**

- `pkg/ai/` — extend `FeatureKey` enum with `FeatureExtractKeywords`. Add `prompts/extract_keywords.txt`.
- `api/service/aiService.go` — no changes (reuses `RunStructuredGeneration`).
- `api/service/keywordExtractionService.go` — **new**. Public entrypoints: `EnqueueForFlashcard(fcId)`, `EnqueueBatch(fcIds)`, `MaterialChange(oldQ, oldA, newQ, newA) bool`.
- `internal/keywordWorker/` — **new**. Owns the goroutine pool, the polling loop, the rate limiter, and the per-job AI call. Singleton started from `main.go`.
- `api/service/flashcardService.go` — call sites: after successful create and after successful update, call `EnqueueForFlashcard`. Update path guards with `MaterialChange`.
- `api/migrations/` — new migration creating `ai_extraction_jobs`, `flashcard_keywords`, and the backfill enqueue.

**Database:**

- `ai_extraction_jobs` — queue rows (see §4.1).
- `flashcard_keywords` — the index itself (see §4.2).

**Frontend:** no changes.

### 3.2 Hard boundaries

- Only `keywordExtractionService` writes to `ai_extraction_jobs` and `flashcard_keywords`.
- Only `keywordWorker` transitions job rows out of `pending`.
- Frontend has no visibility into extraction state; there is no "keywords pending" affordance.
- The worker is the only module besides Spec A that invokes `RunStructuredGeneration`.

## 4. Data Model

### 4.1 `ai_extraction_jobs`

```sql
CREATE TABLE ai_extraction_jobs (
    id           BIGSERIAL PRIMARY KEY,
    fc_id        BIGINT       NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    priority     SMALLINT     NOT NULL DEFAULT 0,
    state        TEXT         NOT NULL CHECK (state IN ('pending','running','done','failed')),
    attempts     SMALLINT     NOT NULL DEFAULT 0,
    last_error   TEXT,
    enqueued_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);

-- Queue dedup: only one in-flight job per FC
CREATE UNIQUE INDEX uniq_extraction_in_flight
    ON ai_extraction_jobs (fc_id)
    WHERE state IN ('pending', 'running');

-- Worker pick order
CREATE INDEX idx_extraction_pickup
    ON ai_extraction_jobs (state, priority DESC, enqueued_at ASC)
    WHERE state = 'pending';
```

**Priorities:**
- `1` — user-triggered create/update (default)
- `0` — re-enqueue after transient failure
- `-1` — migration backfill

**State machine:**

```
pending --worker claim--> running --success--> done
                                 \--failure--> failed (terminal after N attempts)
                                               OR --retry--> pending (priority=0)
```

### 4.2 `flashcard_keywords`

```sql
CREATE TABLE flashcard_keywords (
    fc_id   BIGINT  NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword TEXT    NOT NULL,
    weight  REAL    NOT NULL DEFAULT 1.0 CHECK (weight >= 0 AND weight <= 1),
    PRIMARY KEY (fc_id, keyword)
);

CREATE INDEX idx_keywords_lookup ON flashcard_keywords (keyword);
```

Writes are always "replace all rows for this fc_id" inside a single transaction:

```sql
BEGIN;
DELETE FROM flashcard_keywords WHERE fc_id = $1;
INSERT INTO flashcard_keywords (fc_id, keyword, weight) VALUES ...;
COMMIT;
```

No versioning. Rebuild is idempotent.

## 5. AI Contract

### 5.1 Feature key

```go
const FeatureExtractKeywords FeatureKey = "extract_keywords"
```

### 5.2 Prompt (template at `pkg/ai/prompts/extract_keywords.txt`)

Input fields provided by the worker:
- `{{title}}` — flashcard title (≤200 chars after trim)
- `{{question}}` — markdown, truncated to 1000 chars
- `{{answer}}` — markdown, truncated to 1000 chars

Instruction summary (exact wording lives in the template):
> Extract 5–12 concise topical keywords that capture the core concepts of this flashcard. Prefer nouns and noun phrases. Assign each keyword a weight from 0 to 1 representing how central it is to the card's content. Output JSON only.

### 5.3 Response schema (JSON schema enforced by pipeline)

```json
{
  "type": "object",
  "required": ["keywords"],
  "properties": {
    "keywords": {
      "type": "array",
      "minItems": 1,
      "maxItems": 12,
      "items": {
        "type": "object",
        "required": ["keyword", "weight"],
        "properties": {
          "keyword": { "type": "string", "minLength": 1, "maxLength": 64 },
          "weight":  { "type": "number", "minimum": 0, "maximum": 1 }
        }
      }
    }
  }
}
```

### 5.4 Post-processing

- Lowercase and trim each keyword.
- Collapse whitespace runs to single space.
- Drop duplicates (keep highest weight).
- Drop any keyword >64 chars.
- If `<1` keyword survives, mark job `failed` with `last_error='empty_after_cleanup'` (does not retry).

## 6. Control Flow

### 6.1 Enqueue paths

**FC create** (in `flashcardService.Create`):
```go
fc, err := repo.Create(...)
if err != nil { return err }
keywordExtractionService.EnqueueForFlashcard(ctx, fc.ID, PriorityUser)
return nil
```
Enqueue is best-effort. A failed enqueue logs and continues; the job can be re-triggered from the next update or an admin reconcile.

**FC update** (in `flashcardService.Update`):
```go
old := repo.Get(id)
new := repo.Update(...)
if keywordExtractionService.MaterialChange(old.Question, old.Answer, new.Question, new.Answer) {
    keywordExtractionService.EnqueueForFlashcard(ctx, new.ID, PriorityUser)
}
```

**Dedup implementation** (inside `EnqueueForFlashcard`):
```sql
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
VALUES ($1, $2, 'pending')
ON CONFLICT (fc_id) WHERE state IN ('pending','running')
DO UPDATE SET priority = GREATEST(ai_extraction_jobs.priority, EXCLUDED.priority),
              updated_at = NOW();
```

### 6.2 `MaterialChange(oldQ, oldA, newQ, newA) bool`

Combine Q+A for each side:
```
oldCombined = oldQ + "\x00" + oldA
newCombined = newQ + "\x00" + newA
```

Return `true` iff **both**:
- `abs(len(newCombined) - len(oldCombined)) >= 20` **OR** the byte-diff magnitude ≥20 chars under Myers diff,
- `levenshteinRatio(oldCombined, newCombined) >= 0.10`.

(Using a capped Levenshtein — bail out after 10% computed — to avoid O(n²) blowups on large cards.)

### 6.3 Worker loop

`internal/keywordWorker/Worker` runs `N` goroutines (`N=2` default, env-tunable) plus one poller goroutine:

```
poller:
  loop {
    sleep(if no jobs) backoff 500ms → 5s
    SELECT ... FOR UPDATE SKIP LOCKED LIMIT (N - busy) → mark 'running'
    send to workerChan
  }

worker goroutine:
  for job in workerChan {
    rateLimiter.Wait(ctx)  // blocks until token available
    run(job)
  }

run(job):
  fc := flashcardRepo.Get(job.fc_id)
  if fc == nil { mark done (FC deleted); return }
  resp, err := aiService.RunStructuredGeneration(ctx, FeatureExtractKeywords, ...)
  if err != nil { handleFailure(job, err); return }
  keywords := postprocess(resp.keywords)
  if len(keywords) < 1 { mark failed 'empty_after_cleanup'; return }
  replaceKeywordsTx(job.fc_id, keywords)
  mark done
```

### 6.4 Rate limiting

System-wide `golang.org/x/time/rate.Limiter`:
- Refill: **60 tokens / minute** (1/sec average)
- Burst: **120 tokens**
- Shared across all worker goroutines

Both numbers are env-tunable (`KEYWORD_EXTRACT_RATE_PER_MIN`, `KEYWORD_EXTRACT_BURST`). When the bucket is empty, worker goroutines block — the queue absorbs pressure.

### 6.5 Cost accounting

- Pipeline call does **not** invoke `aiQuotaService.Debit`. Feature-key short-circuits quota for `FeatureExtractKeywords`.
- Token usage is still logged to `ai_audit_log` (already written by `RunStructuredGeneration`) so spend is observable post-hoc.

## 7. Error Handling & Retry

| Failure | Handling |
|---------|----------|
| Provider 5xx / 429 / timeout | Retry with backoff: attempt 1 immediate, attempt 2 after 5s, attempt 3 after 30s. After 3 failed attempts → state `failed`. |
| Provider 4xx (non-429) | Terminal. Mark `failed`, store body snippet in `last_error`. No retry. |
| Schema validation failure | Terminal. Mark `failed` with `last_error='schema_invalid'`. |
| Post-processing yields <1 keyword | Terminal. Mark `failed` with `last_error='empty_after_cleanup'`. |
| Flashcard deleted while job in-flight | Cascade handles rows. Worker treats `fc == nil` as success (mark `done`). |
| Worker crash mid-run | Row stuck in `running`. Reaper goroutine: every 60s, reset `running` jobs with `started_at < NOW() - 5m` back to `pending` with `attempts += 1`. |
| Rate limiter starved | Worker blocks; no quota charged; no retry counter bumped. |

Retries happen by re-enqueue (new `pending` row replaces `failed` state via the dedup conflict path). `failed` rows are kept indefinitely for observability but never re-polled by the worker.

## 8. Backfill

Migration that creates the tables ends with:

```sql
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
SELECT id, -1, 'pending' FROM flashcards
ON CONFLICT (fc_id) WHERE state IN ('pending','running') DO NOTHING;
```

Because priority is `-1`, backfill yields to any live user-triggered work (priority `1`). The rate limiter paces total throughput. No separate CLI tool needed.

## 9. Observability

- **Metrics** (via existing Prometheus hooks):
  - `keyword_extraction_jobs_total{state}` — counter
  - `keyword_extraction_duration_seconds` — histogram
  - `keyword_extraction_queue_depth{priority}` — gauge (sampled every 30s)
  - `keyword_extraction_rate_limit_wait_seconds` — histogram
- **Structured logs** on every job transition: `fc_id`, `state`, `attempts`, `duration_ms`, `keyword_count`, `err`.
- **SQL probes** (runbook):
  - Queue depth: `SELECT state, COUNT(*) FROM ai_extraction_jobs GROUP BY state`
  - Stuck jobs: `SELECT * FROM ai_extraction_jobs WHERE state='running' AND started_at < NOW() - INTERVAL '5 min'`
  - Recent failures: `SELECT * FROM ai_extraction_jobs WHERE state='failed' ORDER BY updated_at DESC LIMIT 50`

## 10. Testing

### Unit (`api/service/`)
- `MaterialChange`: typo-only change → false; whitespace trim → false; 20-char addition → true; paragraph rewrite → true; identity → false; empty → empty → false.
- Post-processing: dedup with case variants, whitespace collapse, max-length trim, empty-after-cleanup.
- Enqueue dedup: two rapid `EnqueueForFlashcard` calls produce one row; priority is the max of the two.

### Integration (`api/service/` with real DB)
- Full happy path: create FC → enqueue → worker picks → AI mock returns fixture → `flashcard_keywords` populated → job `done`.
- Retry path: AI mock fails with 500 twice, succeeds on 3rd → job `done`, `attempts=3`.
- Terminal failure: AI mock returns 400 → job `failed`, no retry attempted.
- Cascade delete: delete FC with pending job → job row gone, keyword rows gone.
- Reaper: `running` job older than 5m → reset to `pending`.

### Worker (`internal/keywordWorker/`)
- Rate limiter: 130 jobs enqueued → first 120 dispatched inside 1s (burst), remaining paced at 1/s.
- Shutdown: context cancel during in-flight job → job marked `pending` for retry, no data written.
- Concurrency: `N=4` workers, no deadlocks under sustained load (1000-job soak).

### Backfill
- Migration on seeded DB with 500 FCs → exactly 500 pending rows, all priority `-1`.
- Re-running migration (idempotency guard) is not required; migrations run once, but the `ON CONFLICT DO NOTHING` makes re-run safe.

## 11. Out of Scope (Deferred)

- User-visible keyword surfaces (tag autocomplete, chip UI, search-by-keyword).
- Manual re-extract button.
- Per-subject keyword dictionaries / canonicalization.
- Cross-language keyword handling beyond what the model emits by default.
- Synonym collapsing / embedding-based similarity (Spec B uses exact keyword overlap; embeddings are a separate future spec).
- Billing / subscription gating — this feature is universal in v1.

## 12. Open Questions (Non-Blocking)

- Worker count and rate-limiter constants (`N=2`, `60/min`, burst `120`) are starting guesses. Tune after first week of production traffic using the Prometheus metrics above.
- Whether to expose `GET /admin/extraction-queue` for internal dashboards — can be added post-launch without schema change.
