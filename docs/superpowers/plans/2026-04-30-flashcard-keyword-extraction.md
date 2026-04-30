# Flashcard Keyword Extraction (Spec B.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the asynchronous keyword extraction subsystem that indexes every flashcard with 5–12 weighted keywords, enabling Spec B's cross-subject shortlist to run as a cheap SQL join.

**Architecture:** A DB-backed job queue (`ai_extraction_jobs`) is fed on flashcard create/update by a thin enqueue helper. A pool of worker goroutines (rate-limited, system-side) polls the queue, calls Anthropic via the existing `aipipeline.Service`, and replaces the per-card row set in `flashcard_keywords`. No HTTP endpoints, no per-user quota — invisible system plumbing.

**Tech Stack:** Go, pgx, `golang.org/x/time/rate` (new dep), the existing `pkg/aipipeline` pipeline primitive, `internal/keywordWorker` (currently a stub).

---

## Decisions Locked Before Drafting

- **Schema realignment is destructive.** Pre-launch — drop and recreate `ai_extraction_jobs` and `flashcard_keywords` with the spec shape. No data to preserve.
- **No quota debit.** `FeatureExtractKeywords` short-circuits `CheckQuota`/`DebitQuota` (system-side cost; the spec is explicit). The pre-staged `extract_keywords_calls` column on `ai_quota_daily` stays unwired for now.
- **Service split:**
  - `pkg/aipipeline/service_keywords.go` exposes `ExtractKeywords(ctx, in) (*KeywordResult, error)` — owns the prompt, schema, and provider call. Mirrors `CheckFlashcard`.
  - `internal/keywordWorker/` owns the queue: enqueue, dedup, `MaterialChange`, post-process, poll loop, rate limiter, reaper.
- **Flashcard service decouples via interface.** `pkg/flashcard.Service` gains a `keywordEnqueuer` interface field; `internal/keywordWorker` implements it. Flashcard tests pass a no-op.
- **Levenshtein:** small in-package implementation with a 10% bail-out cap. No new dep.
- **Rate limiter dep:** `golang.org/x/time/rate` (transitive of many things; small, zero-risk).

---

## File Structure

**Create:**
- `pkg/aipipeline/service_keywords.go` — `ExtractKeywords` method, post-process delegated, schema fn
- `pkg/aipipeline/prompts/extract_keywords.tmpl` — prompt template
- `internal/keywordWorker/enqueue.go` — `Enqueuer` (impl of the flashcard interface), `EnqueueForFlashcard`, `MaterialChange`
- `internal/keywordWorker/postprocess.go` — keyword normalization
- `internal/keywordWorker/queries.go` — SQL constants
- `internal/keywordWorker/reaper.go` — stuck-job reset
- `internal/keywordWorker/poller.go` — claim loop
- `internal/keywordWorker/runner.go` — per-job runner (calls aipipeline, writes keywords, marks done/failed)
- `pkg/aipipeline/service_keywords_test.go`
- `internal/keywordWorker/enqueue_test.go`
- `internal/keywordWorker/postprocess_test.go`
- `internal/keywordWorker/runner_test.go` (integration)

**Modify:**
- `db_sql/setup_ai.go:43-63` — replace `ai_extraction_jobs` and `flashcard_keywords` definitions
- `pkg/aipipeline/model.go:13-20` — add `FeatureExtractKeywords` constant
- `pkg/aipipeline/prompts.go` — add `ExtractKeywordsValues` + `RenderExtractKeywords`
- `internal/keywordWorker/worker.go` — replace stub with real `Worker` struct + `Start`/`Stop`
- `pkg/flashcard/service.go:17-25` — add `keywordEnqueuer` field + interface, plumb constructor
- `pkg/flashcard/service.go:28-46` (Create) — call `enqueuer.EnqueueForFlashcard` after insert
- `pkg/flashcard/service.go:82-110` (Update) — call `MaterialChange` then enqueue if true
- `cmd/app/deps.go:122-131` (`buildInfra`) — construct real `keywordWorker.Worker` with the aipipeline service + rate limiter
- `cmd/app/deps.go:152-169` (`buildDomainServices`) — pass enqueuer into `flashcard.NewService`
- `cmd/app/main.go` — register the worker's reaper as a cron job; ensure `worker.Start(ctx)` runs

---

## Task 1: Realign `ai_extraction_jobs` and `flashcard_keywords` schema

**Why first:** every later task assumes the spec column names (`fc_id`, `state`, `priority`).

**Files:**
- Modify: `db_sql/setup_ai.go:43-63`

- [ ] **Step 1: Read the current file** to confirm anchor lines

```bash
sed -n '40,65p' db_sql/setup_ai.go
```

- [ ] **Step 2: Replace the `ai_extraction_jobs` and `flashcard_keywords` block**

In `db_sql/setup_ai.go`, delete lines 43–63 (the `CREATE TABLE ai_extraction_jobs` block, its index, the `flashcard_keywords` block, and its index). Replace with:

```go
DROP TABLE IF EXISTS flashcard_keywords;
DROP TABLE IF EXISTS ai_extraction_jobs;

CREATE TABLE ai_extraction_jobs (
    id           BIGSERIAL    PRIMARY KEY,
    fc_id        BIGINT       NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    priority     SMALLINT     NOT NULL DEFAULT 0,
    state        TEXT         NOT NULL CHECK (state IN ('pending','running','done','failed')),
    attempts     SMALLINT     NOT NULL DEFAULT 0,
    last_error   TEXT,
    enqueued_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX uniq_extraction_in_flight
    ON ai_extraction_jobs (fc_id)
    WHERE state IN ('pending','running');
CREATE INDEX idx_extraction_pickup
    ON ai_extraction_jobs (priority DESC, enqueued_at ASC)
    WHERE state = 'pending';

CREATE TABLE flashcard_keywords (
    fc_id   BIGINT  NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword TEXT    NOT NULL,
    weight  REAL    NOT NULL DEFAULT 1.0 CHECK (weight >= 0 AND weight <= 1),
    PRIMARY KEY (fc_id, keyword)
);
CREATE INDEX idx_flashcard_keywords_kw ON flashcard_keywords(keyword);
```

The `DROP TABLE IF EXISTS` lines are intentional — boot reruns the schema on every start; we accept the rebuild on the next launch since pre-launch and dev-only.

- [ ] **Step 3: Build to confirm no syntax errors**

Run: `go build ./...`
Expected: clean exit.

- [ ] **Step 4: Restart the dev DB so the schema rebuilds**

Run: `./setup_db.sh` (drops and re-creates) — or whatever the project's reset path is. Confirm the new tables match the spec via `psql`:

```bash
psql -d studbud_dev -c '\d ai_extraction_jobs'
psql -d studbud_dev -c '\d flashcard_keywords'
```

Expected: `state` and `fc_id` columns present, `uniq_extraction_in_flight` index visible.

- [ ] **Step 5: Commit**

```bash
git add db_sql/setup_ai.go
git commit -m "$(cat <<'EOF'
Realign keyword-extraction schema to Spec B.0

[&] ai_extraction_jobs uses fc_id/state/priority shape from spec
[&] flashcard_keywords uses fc_id column name
[+] uniq_extraction_in_flight partial unique on (fc_id)
[+] idx_extraction_pickup partial index on pending jobs
EOF
)"
```

---

## Task 2: Add `FeatureExtractKeywords` to the FeatureKey enum

**Files:**
- Modify: `pkg/aipipeline/model.go:13-20`

- [ ] **Step 1: Add the constant**

In `pkg/aipipeline/model.go`, edit the `const` block from line 13 to add a fourth feature key:

```go
const (
	FeatureGenerateFromPrompt FeatureKey = "generate_prompt"
	FeatureGenerateFromPDF    FeatureKey = "generate_pdf"
	FeatureCheckFlashcard     FeatureKey = "check_flashcard"
	FeatureExtractKeywords    FeatureKey = "extract_keywords"
)
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean exit.

- [ ] **Step 3: Commit**

```bash
git add pkg/aipipeline/model.go
git commit -m "$(cat <<'EOF'
Add FeatureExtractKeywords feature key

[+] FeatureExtractKeywords constant for B.0 keyword index
EOF
)"
```

---

## Task 3: Add the `extract_keywords.tmpl` prompt template

**Files:**
- Create: `pkg/aipipeline/prompts/extract_keywords.tmpl`

- [ ] **Step 1: Write the template**

Create `pkg/aipipeline/prompts/extract_keywords.tmpl` with:

```
Extract topical keywords from this flashcard. Output JSON only.

LANGUAGE: detect the language used in the title/question/answer below and emit keywords in THAT SAME language. Do not translate. If languages mix, use the dominant one.

Rules:
- Return between 5 and 12 keywords.
- Prefer concise nouns or noun phrases (1–3 words).
- Lowercase each keyword.
- Assign each keyword a "weight" between 0 and 1: 1 = central concept, 0 = barely relevant.
- Drop filler words ("the", "a", connectors).
- Do not invent concepts that are not present in the card.

Flashcard:
- Title: {{.Title}}
- Question: {{.Question}}
- Answer: {{.Answer}}

Output a JSON object: { "keywords": [ { "keyword": string, "weight": number }, ... ] }.

Output ONLY the JSON object, with no surrounding prose, no markdown code fences, no commentary. Begin your response with `{` and end with `}`.
```

- [ ] **Step 2: Build to confirm `embed.FS` picks it up**

Run: `go build ./...`
Expected: clean exit. (The `//go:embed prompts/*.tmpl` directive in `prompts.go` already includes any `.tmpl` file.)

- [ ] **Step 3: Commit**

```bash
git add pkg/aipipeline/prompts/extract_keywords.tmpl
git commit -m "$(cat <<'EOF'
Add extract_keywords prompt template

[+] prompts/extract_keywords.tmpl for FeatureExtractKeywords
EOF
)"
```

---

## Task 4: Add `ExtractKeywordsValues` and `RenderExtractKeywords`

**Files:**
- Modify: `pkg/aipipeline/prompts.go` (add types and exported renderer at end of file)

- [ ] **Step 1: Write the failing test**

Append to `pkg/aipipeline/prompts_test.go`:

```go
func TestRenderExtractKeywords_EmbedsAllFields(t *testing.T) {
	out, err := RenderExtractKeywords(ExtractKeywordsValues{
		Title:    "Mitose",
		Question: "Quelles sont les phases de la mitose ?",
		Answer:   "Prophase, métaphase, anaphase, télophase.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Mitose", "phases de la mitose", "Prophase", "keywords"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/aipipeline/ -run TestRenderExtractKeywords -count=1`
Expected: `undefined: RenderExtractKeywords`.

- [ ] **Step 3: Add the type and renderer**

In `pkg/aipipeline/prompts.go`, append after the `CheckValues` definition (around line 93):

```go
// ExtractKeywordsValues is the template input for the keyword-extraction feature.
type ExtractKeywordsValues struct {
	Title    string // Title is the flashcard title (may be empty)
	Question string // Question is the flashcard prompt
	Answer   string // Answer is the flashcard answer
}
```

And append at the very end of the file (after the `RenderCheck` line):

```go
// RenderExtractKeywords is the exported wrapper for the keyword-extraction template.
func RenderExtractKeywords(v ExtractKeywordsValues) (string, error) {
	return renderTemplate("prompts/extract_keywords.tmpl", v)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/aipipeline/ -run TestRenderExtractKeywords -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/prompts.go pkg/aipipeline/prompts_test.go
git commit -m "$(cat <<'EOF'
Add RenderExtractKeywords renderer

[+] ExtractKeywordsValues template input type
[+] RenderExtractKeywords exported wrapper
[+] test for prompt rendering
EOF
)"
```

---

## Task 5: Add `ExtractKeywords` method on `aipipeline.Service`

**Files:**
- Create: `pkg/aipipeline/service_keywords.go`
- Create: `pkg/aipipeline/service_keywords_test.go`

This task wires the prompt + provider call. Quota is NOT debited; the only thing this method does is render → call provider → parse → return. The worker will store the result and mark the job.

- [ ] **Step 1: Write the failing test**

Create `pkg/aipipeline/service_keywords_test.go`:

```go
package aipipeline

import (
	"context"
	"strings"
	"testing"

	"studbud/backend/internal/aiProvider"
)

type fakeKeywordProvider struct {
	body string
}

func (f *fakeKeywordProvider) Stream(ctx context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	ch := make(chan aiProvider.Chunk, 1)
	ch <- aiProvider.Chunk{Text: f.body, Done: true}
	close(ch)
	return ch, nil
}

func TestExtractKeywords_HappyPath(t *testing.T) {
	body := `{"keywords":[{"keyword":"mitose","weight":1.0},{"keyword":"chromosome","weight":0.7}]}`
	svc := &Service{provider: &fakeKeywordProvider{body: body}, model: "claude-test"}
	out, err := svc.ExtractKeywords(context.Background(), ExtractInput{
		Title:    "Mitose",
		Question: "Q",
		Answer:   "A",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(out.Keywords) != 2 {
		t.Fatalf("want 2 keywords, got %d", len(out.Keywords))
	}
	if out.Keywords[0].Keyword != "mitose" || out.Keywords[0].Weight != 1.0 {
		t.Errorf("first kw mismatch: %+v", out.Keywords[0])
	}
}

func TestExtractKeywords_BadJSON(t *testing.T) {
	svc := &Service{provider: &fakeKeywordProvider{body: "not json"}, model: "claude-test"}
	_, err := svc.ExtractKeywords(context.Background(), ExtractInput{Question: "Q", Answer: "A"})
	if err == nil || !strings.Contains(err.Error(), "parse keywords") {
		t.Fatalf("want parse error, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/aipipeline/ -run TestExtractKeywords -count=1`
Expected: `undefined: ExtractKeywords` / `undefined: ExtractInput`.

- [ ] **Step 3: Implement `ExtractKeywords`**

Create `pkg/aipipeline/service_keywords.go`:

```go
package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/aiProvider"
)

// ExtractInput describes one keyword-extraction request.
type ExtractInput struct {
	Title    string // Title is the flashcard title (may be empty)
	Question string // Question is the flashcard prompt
	Answer   string // Answer is the flashcard answer
}

// ExtractedKeyword is one keyword/weight pair from the model.
type ExtractedKeyword struct {
	Keyword string  `json:"keyword"` // Keyword is the topical token (raw, pre-postprocess)
	Weight  float64 `json:"weight"`  // Weight is the model-assigned 0..1 centrality
}

// KeywordResult is the parsed model output.
type KeywordResult struct {
	Keywords []ExtractedKeyword `json:"keywords"` // Keywords is the unprocessed list
}

// ExtractKeywords runs a non-streaming keyword extraction call against the provider.
// It does NOT touch the quota tables (system-side cost) and does NOT insert ai_jobs
// rows — it is the lowest-level primitive used by the keyword worker.
func (s *Service) ExtractKeywords(ctx context.Context, in ExtractInput) (*KeywordResult, error) {
	prompt, err := RenderExtractKeywords(ExtractKeywordsValues{
		Title:    in.Title,
		Question: in.Question,
		Answer:   in.Answer,
	})
	if err != nil {
		return nil, fmt.Errorf("render extract prompt:\n%w", err)
	}
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(FeatureExtractKeywords),
		Model:      s.model,
		Prompt:     prompt,
		Schema:     extractKeywordsSchema(),
		MaxTokens:  1024,
	})
	if err != nil {
		return nil, classifyProviderStartErr(err)
	}
	buf, err := drainChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}
	var out KeywordResult
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse keywords:\n%w", err)
	}
	return &out, nil
}

// extractKeywordsSchema returns the tool-use JSON schema for keyword extraction.
func extractKeywordsSchema() []byte {
	return []byte(`{
      "type":"object",
      "required":["keywords"],
      "properties":{
        "keywords":{
          "type":"array",
          "minItems":1,
          "maxItems":12,
          "items":{
            "type":"object",
            "required":["keyword","weight"],
            "properties":{
              "keyword":{"type":"string","minLength":1,"maxLength":64},
              "weight":{"type":"number","minimum":0,"maximum":1}
            }
          }
        }
      }
    }`)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/aipipeline/ -run TestExtractKeywords -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aipipeline/service_keywords.go pkg/aipipeline/service_keywords_test.go
git commit -m "$(cat <<'EOF'
Add aipipeline.Service.ExtractKeywords

[+] ExtractKeywords method (non-streaming, no quota debit)
[+] ExtractInput, KeywordResult, ExtractedKeyword types
[+] extractKeywordsSchema tool-use JSON schema
[+] unit tests for happy path and malformed JSON
EOF
)"
```

---

## Task 6: Implement keyword post-processing

**Files:**
- Create: `internal/keywordWorker/postprocess.go`
- Create: `internal/keywordWorker/postprocess_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/keywordWorker/postprocess_test.go`:

```go
package keywordWorker

import (
	"testing"

	"studbud/backend/pkg/aipipeline"
)

func TestPostprocess_LowercasesAndTrims(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{
		{Keyword: "  Mitose  ", Weight: 0.9},
		{Keyword: "Chromosome", Weight: 0.7},
	}
	out := postprocess(in)
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0].Keyword != "mitose" {
		t.Errorf("not lowercased/trimmed: %q", out[0].Keyword)
	}
}

func TestPostprocess_CollapsesWhitespace(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{{Keyword: "cycle   cellulaire", Weight: 0.5}}
	out := postprocess(in)
	if out[0].Keyword != "cycle cellulaire" {
		t.Errorf("whitespace not collapsed: %q", out[0].Keyword)
	}
}

func TestPostprocess_DedupesKeepingHighestWeight(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{
		{Keyword: "Mitose", Weight: 0.4},
		{Keyword: "mitose", Weight: 0.9},
		{Keyword: " mitose ", Weight: 0.2},
	}
	out := postprocess(in)
	if len(out) != 1 {
		t.Fatalf("want 1 deduped, got %d", len(out))
	}
	if out[0].Weight != 0.9 {
		t.Errorf("want highest weight 0.9, got %v", out[0].Weight)
	}
}

func TestPostprocess_DropsOver64Chars(t *testing.T) {
	long := make([]byte, 70)
	for i := range long {
		long[i] = 'a'
	}
	in := []aipipeline.ExtractedKeyword{
		{Keyword: string(long), Weight: 0.9},
		{Keyword: "ok", Weight: 0.5},
	}
	out := postprocess(in)
	if len(out) != 1 || out[0].Keyword != "ok" {
		t.Errorf("did not drop oversized: %+v", out)
	}
}

func TestPostprocess_EmptyAfterCleanup(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{{Keyword: "   ", Weight: 0.5}}
	out := postprocess(in)
	if len(out) != 0 {
		t.Errorf("want empty, got %+v", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/keywordWorker/ -count=1`
Expected: `undefined: postprocess`.

- [ ] **Step 3: Implement `postprocess`**

Create `internal/keywordWorker/postprocess.go`:

```go
package keywordWorker

import (
	"strings"

	"studbud/backend/pkg/aipipeline"
)

// postprocess normalizes a model-emitted keyword list.
// Rules: lowercase, trim, whitespace-collapse, dedup-keep-max-weight, drop >64 chars,
// drop empties. Returns the cleaned slice (may be empty).
func postprocess(in []aipipeline.ExtractedKeyword) []aipipeline.ExtractedKeyword {
	byKey := make(map[string]float64, len(in))
	for _, k := range in {
		clean := normalize(k.Keyword)
		if clean == "" || len(clean) > 64 {
			continue
		}
		if existing, ok := byKey[clean]; !ok || k.Weight > existing {
			byKey[clean] = k.Weight
		}
	}
	out := make([]aipipeline.ExtractedKeyword, 0, len(byKey))
	for k, w := range byKey {
		out = append(out, aipipeline.ExtractedKeyword{Keyword: k, Weight: w})
	}
	return out
}

// normalize lowercases, trims, and collapses internal whitespace runs.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/keywordWorker/ -count=1`
Expected: PASS (5/5).

- [ ] **Step 5: Commit**

```bash
git add internal/keywordWorker/postprocess.go internal/keywordWorker/postprocess_test.go
git commit -m "$(cat <<'EOF'
Add keyword post-processing

[+] postprocess normalizes/dedupes model-emitted keywords
[+] unit tests for trim, whitespace collapse, dedup, length cap, empty
EOF
)"
```

---

## Task 7: Implement `MaterialChange`

**Files:**
- Create: `internal/keywordWorker/enqueue.go` (partial — only `MaterialChange` for this task)

- [ ] **Step 1: Write the failing test**

Append to `internal/keywordWorker/postprocess_test.go` (or create `enqueue_test.go`; reusing the test file is fine for the same package):

Actually — create a separate file for clarity. Create `internal/keywordWorker/enqueue_test.go`:

```go
package keywordWorker

import "testing"

func TestMaterialChange_Identity(t *testing.T) {
	if MaterialChange("q", "a", "q", "a") {
		t.Error("identity should not be material")
	}
}

func TestMaterialChange_TrailingWhitespace(t *testing.T) {
	if MaterialChange("hello", "world", "hello ", "world") {
		t.Error("trailing whitespace should not be material")
	}
}

func TestMaterialChange_TwentyCharAddition(t *testing.T) {
	old := "Quelle est la phase ?"
	newQ := "Quelle est la phase de la mitose dans le cycle cellulaire ?"
	if !MaterialChange(old, "answer", newQ, "answer") {
		t.Error("20+ char addition should be material")
	}
}

func TestMaterialChange_FullRewrite(t *testing.T) {
	if !MaterialChange("foo", "bar", "completely different content here please", "ok") {
		t.Error("full rewrite should be material")
	}
}

func TestMaterialChange_EmptyToEmpty(t *testing.T) {
	if MaterialChange("", "", "", "") {
		t.Error("empty to empty should not be material")
	}
}

func TestMaterialChange_TypoFix(t *testing.T) {
	if MaterialChange("Quelle est la phase ?", "Phase A", "Quelle est la phase ?", "Phase A.") {
		t.Error("trailing period should not be material")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/keywordWorker/ -run MaterialChange -count=1`
Expected: `undefined: MaterialChange`.

- [ ] **Step 3: Implement `MaterialChange`**

Create `internal/keywordWorker/enqueue.go`:

```go
package keywordWorker

import "math"

// MaterialChange returns true when the (oldQ,oldA) → (newQ,newA) edit is large enough
// that re-extracting keywords is worth the AI call.
//
// The rule: BOTH must hold —
//  1. absolute byte-length delta of the joined Q+A is >= 20 chars, AND
//  2. levenshteinRatio(old, new) >= 0.10.
//
// This avoids re-extraction on typo fixes and trailing-whitespace edits while
// still catching meaningful additions or rewrites.
func MaterialChange(oldQ, oldA, newQ, newA string) bool {
	const sep = "\x00"
	oldCombined := oldQ + sep + oldA
	newCombined := newQ + sep + newA
	if oldCombined == newCombined {
		return false
	}
	lenDelta := abs(len(newCombined) - len(oldCombined))
	if lenDelta < 20 {
		return false
	}
	ratio := levenshteinRatio(oldCombined, newCombined)
	return ratio >= 0.10
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshteinRatio returns the edit distance divided by the longer string's length.
// Bounded by an early-exit cap of ceil(0.1 * maxLen) — any distance above that is
// reported as the cap (caller treats it as material). Keeps cost ~O(n * cap).
func levenshteinRatio(a, b string) float64 {
	maxLen := math.Max(float64(len(a)), float64(len(b)))
	if maxLen == 0 {
		return 0
	}
	cap := int(math.Ceil(maxLen * 0.10))
	if cap < 1 {
		cap = 1
	}
	dist := boundedLevenshtein(a, b, cap)
	return float64(dist) / maxLen
}

// boundedLevenshtein computes the edit distance up to `bound`. If the true distance
// exceeds bound, returns bound+1 (signal that the change is at least bound+1).
func boundedLevenshtein(a, b string, bound int) int {
	la, lb := len(a), len(b)
	if abs(la-lb) > bound {
		return bound + 1
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		minRow := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < minRow {
				minRow = curr[j]
			}
		}
		if minRow > bound {
			return bound + 1
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/keywordWorker/ -run MaterialChange -count=1`
Expected: PASS (6/6).

- [ ] **Step 5: Commit**

```bash
git add internal/keywordWorker/enqueue.go internal/keywordWorker/enqueue_test.go
git commit -m "$(cat <<'EOF'
Add MaterialChange edit-significance check

[+] MaterialChange compares Q+A pairs for re-extraction worthiness
[+] bounded Levenshtein with 10% early-exit cap
[+] unit tests for identity, whitespace, addition, rewrite, typo
EOF
)"
```

---

## Task 8: Add `Enqueuer` and `EnqueueForFlashcard` (DB-backed dedup)

**Files:**
- Modify: `internal/keywordWorker/enqueue.go` (add Enqueuer struct + method)
- Create: `internal/keywordWorker/queries.go`

This task adds the SQL constants and the dedup INSERT.

- [ ] **Step 1: Write the integration test**

Append to `internal/keywordWorker/enqueue_test.go`:

```go
import (
	"context"
	"studbud/backend/testutil"
)

func TestEnqueueForFlashcard_DedupesAndKeepsMaxPriority(t *testing.T) {
	pool := testutil.NewPool(t)
	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.CreateSubject(t, pool, uid, "Bio")
	fc := testutil.CreateFlashcard(t, pool, subj, "T", "Q", "A")

	enq := &Enqueuer{db: pool}
	ctx := context.Background()

	if err := enq.EnqueueForFlashcard(ctx, fc, PriorityUser); err != nil {
		t.Fatal(err)
	}
	if err := enq.EnqueueForFlashcard(ctx, fc, PriorityRetry); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_extraction_jobs WHERE fc_id=$1`, fc).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 row (deduped), got %d", n)
	}
	var prio int
	if err := pool.QueryRow(ctx, `SELECT priority FROM ai_extraction_jobs WHERE fc_id=$1`, fc).Scan(&prio); err != nil {
		t.Fatal(err)
	}
	if prio != int(PriorityUser) {
		t.Fatalf("want priority kept at PriorityUser=%d, got %d", PriorityUser, prio)
	}
}
```

(If `testutil.CreateFlashcard` or `testutil.CreateSubject` don't exist with these signatures, check the package — the test should mirror existing helpers in `pkg/aipipeline/service_check_test.go`. Adapt the helper invocations to match the real testutil API but keep the assertion structure.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/keywordWorker/ -run TestEnqueueForFlashcard -count=1`
Expected: `undefined: Enqueuer` / `undefined: PriorityUser`.

- [ ] **Step 3: Add the SQL constants and Enqueuer**

Create `internal/keywordWorker/queries.go`:

```go
package keywordWorker

// queries.go centralizes the SQL used by the keyword worker.

const sqlEnqueueJob = `
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
VALUES ($1, $2, 'pending')
ON CONFLICT (fc_id) WHERE state IN ('pending','running')
DO UPDATE SET
  priority   = GREATEST(ai_extraction_jobs.priority, EXCLUDED.priority),
  updated_at = now()
`

const sqlClaimPending = `
UPDATE ai_extraction_jobs
SET state = 'running', started_at = now(), updated_at = now()
WHERE id IN (
  SELECT id FROM ai_extraction_jobs
  WHERE state = 'pending'
  ORDER BY priority DESC, enqueued_at ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
RETURNING id, fc_id, attempts
`

const sqlMarkDone = `
UPDATE ai_extraction_jobs
SET state = 'done', finished_at = now(), updated_at = now()
WHERE id = $1
`

const sqlMarkFailed = `
UPDATE ai_extraction_jobs
SET state = 'failed', last_error = $2, finished_at = now(), updated_at = now()
WHERE id = $1
`

const sqlRequeueAfterError = `
UPDATE ai_extraction_jobs
SET state = 'pending', priority = 0, attempts = attempts + 1,
    last_error = $2, updated_at = now(), started_at = NULL
WHERE id = $1
`

const sqlReplaceKeywordsDelete = `DELETE FROM flashcard_keywords WHERE fc_id = $1`

const sqlReplaceKeywordsInsert = `
INSERT INTO flashcard_keywords (fc_id, keyword, weight) VALUES ($1, $2, $3)
`

const sqlReapStuckRunning = `
UPDATE ai_extraction_jobs
SET state = 'pending', attempts = attempts + 1,
    last_error = 'reaped: running > 5m', updated_at = now(), started_at = NULL
WHERE state = 'running' AND started_at < now() - interval '5 minutes'
`

const sqlBackfillExisting = `
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
SELECT id, -1, 'pending' FROM flashcards
ON CONFLICT (fc_id) WHERE state IN ('pending','running') DO NOTHING
`
```

In `internal/keywordWorker/enqueue.go`, append (after `MaterialChange` and helpers):

```go
import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Priority is an enqueue ordering hint. Higher = picked first.
type Priority int16

const (
	// PriorityBackfill is reserved for migration-time bulk enqueue.
	PriorityBackfill Priority = -1
	// PriorityRetry is used when re-enqueueing after a transient failure.
	PriorityRetry Priority = 0
	// PriorityUser is the default for create/update-triggered jobs.
	PriorityUser Priority = 1
)

// Enqueuer inserts (and dedups) keyword-extraction jobs.
// Implements pkg/flashcard.KeywordEnqueuer.
type Enqueuer struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewEnqueuer constructs an Enqueuer.
func NewEnqueuer(db *pgxpool.Pool) *Enqueuer {
	return &Enqueuer{db: db}
}

// EnqueueForFlashcard inserts a pending job for fcID, or bumps the priority of an
// existing pending/running row to max(existing, prio).
func (e *Enqueuer) EnqueueForFlashcard(ctx context.Context, fcID int64, prio Priority) error {
	if _, err := e.db.Exec(ctx, sqlEnqueueJob, fcID, int16(prio)); err != nil {
		return fmt.Errorf("enqueue extraction job %d:\n%w", fcID, err)
	}
	return nil
}
```

(Adjust the imports at the top of `enqueue.go` to add `context`, `fmt`, and `pgxpool`. Keep the existing math import for `MaterialChange`.)

- [ ] **Step 4: Run the test**

Run: `go test ./internal/keywordWorker/ -run TestEnqueueForFlashcard -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/keywordWorker/enqueue.go internal/keywordWorker/enqueue_test.go internal/keywordWorker/queries.go
git commit -m "$(cat <<'EOF'
Add Enqueuer for keyword extraction jobs

[+] Enqueuer.EnqueueForFlashcard with dedup + priority-max merge
[+] queries.go SQL constants for enqueue/claim/done/fail/reap
[+] Priority enum (Backfill, Retry, User)
[+] integration test for dedup + priority merge
EOF
)"
```

---

## Task 9: Plumb the enqueuer through `flashcard.Service`

**Files:**
- Modify: `pkg/flashcard/service.go:17-25` (struct + constructor)
- Modify: `pkg/flashcard/service.go:28-46` (Create)
- Modify: `pkg/flashcard/service.go:82-110` (Update)

The flashcard service mustn't import `internal/keywordWorker` (would be a layering surprise even though Go allows it from same module). Use an interface defined in `pkg/flashcard/`.

- [ ] **Step 1: Read existing service to confirm anchors**

```bash
sed -n '15,50p' pkg/flashcard/service.go
```

- [ ] **Step 2: Add the interface and inject through the constructor**

In `pkg/flashcard/service.go`, replace the existing `Service` struct and constructor (lines 17–25) with:

```go
// KeywordEnqueuer is the seam the flashcard service uses to trigger keyword
// re-indexing on create/update. Implemented by internal/keywordWorker.
type KeywordEnqueuer interface {
	// EnqueueForFlashcard schedules keyword extraction for fcID.
	// Errors are best-effort; callers log and continue.
	EnqueueForFlashcard(ctx context.Context, fcID int64, prio int16) error
}

// noopEnqueuer is the test/default enqueuer that drops calls silently.
type noopEnqueuer struct{}

func (noopEnqueuer) EnqueueForFlashcard(context.Context, int64, int16) error { return nil }

// Service owns flashcard CRUD and lightweight review tracking.
type Service struct {
	db       *pgxpool.Pool   // db is the shared pool
	access   *access.Service // access enforces subject-scoped permissions
	enqueuer KeywordEnqueuer // enqueuer triggers async keyword re-extraction (best-effort)
}

// NewService constructs a Service. enqueuer may be nil; a no-op is installed.
func NewService(db *pgxpool.Pool, acc *access.Service, enqueuer KeywordEnqueuer) *Service {
	if enqueuer == nil {
		enqueuer = noopEnqueuer{}
	}
	return &Service{db: db, access: acc, enqueuer: enqueuer}
}
```

The interface uses `int16` for the priority instead of importing the worker's `Priority` type — keeps the package free of a dependency on `internal/keywordWorker`. The worker's `EnqueueForFlashcard` signature already takes `Priority` which is `int16` underneath; we add an adapter shim in Task 11.

- [ ] **Step 3: Wire enqueue into Create**

In `pkg/flashcard/service.go`, replace the `Create` method body's tail (after `s.insert(ctx, in)`):

```go
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Flashcard, error) {
	if in.Question == "" || in.Answer == "" {
		return nil, myErrors.ErrInvalidInput
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Source != "manual" && in.Source != "ai" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureEdit(ctx, uid, in.SubjectID); err != nil {
		return nil, err
	}
	fc, err := s.insert(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.enqueuer.EnqueueForFlashcard(ctx, fc.ID, 1); err != nil {
		log.Printf("flashcard.Create: enqueue keyword extraction failed for fc %d: %v", fc.ID, err)
	}
	return fc, nil
}
```

Add `"log"` to the imports of `pkg/flashcard/service.go`.

- [ ] **Step 4: Wire MaterialChange + enqueue into Update**

In the same file, replace the `Update` method's body with one that captures the prior question/answer before patching, and calls the enqueue path after a successful UPDATE if the change is material:

```go
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Flashcard, error) {
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return nil, err
	}
	oldQ, oldA := fc.Question, fc.Answer
	title, question, answer, chapterID, imageID, err := applyFlashcardPatch(fc, in)
	if err != nil {
		return nil, err
	}
	var out Flashcard
	err = s.db.QueryRow(ctx, `
		UPDATE flashcards
		SET chapter_id=$1, title=$2, question=$3, answer=$4, image_id=$5, updated_at=now()
		WHERE id=$6
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, chapterID, title, question, answer, imageID, id).Scan(
		&out.ID, &out.SubjectID, &out.ChapterID, &out.Title, &out.Question, &out.Answer,
		&out.ImageID, &out.Source, &out.DueAt, &out.LastResult, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update flashcard:\n%w", err)
	}
	if shouldReindex(oldQ, oldA, out.Question, out.Answer) {
		if err := s.enqueuer.EnqueueForFlashcard(ctx, out.ID, 1); err != nil {
			log.Printf("flashcard.Update: enqueue keyword extraction failed for fc %d: %v", out.ID, err)
		}
	}
	return &out, nil
}
```

Add at the bottom of the file:

```go
// shouldReindex is the seam used by Update to decide whether keyword extraction
// is worth re-running. Defined as a package-level variable so tests can swap it
// for a deterministic stub. Production points at internal/keywordWorker.MaterialChange.
var shouldReindex = func(oldQ, oldA, newQ, newA string) bool { return true }
```

The `var shouldReindex` indirection lets us avoid importing `internal/keywordWorker` from `pkg/flashcard`. The wiring (point this var at the real `MaterialChange`) happens in `cmd/app/deps.go` in Task 12.

- [ ] **Step 5: Update existing tests in `pkg/flashcard/service_test.go`**

Run: `go vet ./pkg/flashcard/...`
Expected: error about `NewService` arity.

Find every call to `flashcard.NewService(pool, acc)` in `pkg/flashcard/service_test.go` and replace with `flashcard.NewService(pool, acc, nil)`. Also check `cmd/app/deps.go` and any test file that constructs the service.

```bash
grep -rn "flashcard.NewService" --include="*.go"
```

Expected callers (each must pass `nil` until Task 12 wires the real enqueuer):
- `cmd/app/deps.go:161` — passing `nil` is fine for this task; Task 12 fixes it.
- `pkg/flashcard/service_test.go` — all sites pass `nil`.
- Any e2e test wiring.

- [ ] **Step 6: Build and run flashcard tests**

Run:
```bash
go build ./...
go test ./pkg/flashcard/... -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/flashcard/service.go pkg/flashcard/service_test.go cmd/app/deps.go
git commit -m "$(cat <<'EOF'
Wire keyword enqueuer into flashcard.Service

[+] KeywordEnqueuer interface in pkg/flashcard
[+] noopEnqueuer default for tests
[&] flashcard.NewService accepts an enqueuer (nil-safe)
[+] Create best-effort enqueue at PriorityUser
[+] Update calls shouldReindex var, enqueues when material
[+] shouldReindex package var (wired to MaterialChange in cmd/app)
EOF
)"
```

---

## Task 10: Implement the per-job runner

**Files:**
- Create: `internal/keywordWorker/runner.go`
- Create: `internal/keywordWorker/runner_test.go`

The runner is the function that, given one claimed job, executes the AI call and writes the keyword rows.

- [ ] **Step 1: Define the integration test**

Create `internal/keywordWorker/runner_test.go`:

```go
package keywordWorker

import (
	"context"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

type fakeProv struct{ body string }

func (f *fakeProv) Stream(_ context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	ch := make(chan aiProvider.Chunk, 1)
	ch <- aiProvider.Chunk{Text: f.body, Done: true}
	close(ch)
	return ch, nil
}

func TestRunOnce_HappyPath(t *testing.T) {
	pool := testutil.NewPool(t)
	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.CreateSubject(t, pool, uid, "Bio")
	fcID := testutil.CreateFlashcard(t, pool, subj, "Mitose", "Phases?", "Pro/meta/ana/telo.")

	enq := NewEnqueuer(pool)
	if err := enq.EnqueueForFlashcard(context.Background(), fcID, PriorityUser); err != nil {
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
	pool := testutil.NewPool(t)
	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.CreateSubject(t, pool, uid, "Bio")
	fcID := testutil.CreateFlashcard(t, pool, subj, "T", "Q", "A")

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
```

This test uses `aipipeline.NewServiceForTest` which doesn't exist yet — it's a thin testing constructor. Add it next.

- [ ] **Step 2: Add `NewServiceForTest` to `pkg/aipipeline/service.go`**

Append to `pkg/aipipeline/service.go`:

```go
// NewServiceForTest constructs a minimal Service for tests that exercise the
// extraction or check primitives without the entitlement / quota plumbing.
// Production must use NewService.
func NewServiceForTest(db *pgxpool.Pool, provider aiProvider.Client, model string) *Service {
	return &Service{db: db, provider: provider, model: model}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/keywordWorker/ -run TestRunOnce -count=1`
Expected: `undefined: Runner`.

- [ ] **Step 4: Implement `Runner`**

Create `internal/keywordWorker/runner.go`:

```go
package keywordWorker

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/aipipeline"
)

// Runner consumes one claimed job: invokes the AI, post-processes, writes keywords.
type Runner struct {
	db *pgxpool.Pool
	ai *aipipeline.Service
}

// NewRunner constructs a Runner.
func NewRunner(db *pgxpool.Pool, ai *aipipeline.Service) *Runner {
	return &Runner{db: db, ai: ai}
}

// claimedJob is one row returned by sqlClaimPending.
type claimedJob struct {
	id       int64
	fcID     int64
	attempts int16
}

// RunOnce claims at most one pending job and runs it. Returns the number of
// jobs processed (0 or 1). Used by tests; the poller invokes Run repeatedly.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	jobs, err := r.claim(ctx, 1)
	if err != nil {
		return 0, err
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	r.run(ctx, jobs[0])
	return 1, nil
}

// claim runs the FOR UPDATE SKIP LOCKED claim transaction.
func (r *Runner) claim(ctx context.Context, n int) ([]claimedJob, error) {
	rows, err := r.db.Query(ctx, sqlClaimPending, n)
	if err != nil {
		return nil, fmt.Errorf("claim jobs:\n%w", err)
	}
	defer rows.Close()
	var out []claimedJob
	for rows.Next() {
		var j claimedJob
		if err := rows.Scan(&j.id, &j.fcID, &j.attempts); err != nil {
			return nil, fmt.Errorf("scan claimed job:\n%w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// run executes one claimed job to completion (success or failure).
func (r *Runner) run(ctx context.Context, j claimedJob) {
	in, err := r.loadFlashcard(ctx, j.fcID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.markDone(ctx, j.id) // FC deleted; cascade handles keyword rows
			return
		}
		r.markFailed(ctx, j.id, "load_fc:"+err.Error())
		return
	}
	res, err := r.ai.ExtractKeywords(ctx, *in)
	if err != nil {
		r.markFailed(ctx, j.id, "ai:"+err.Error())
		return
	}
	cleaned := postprocess(res.Keywords)
	if len(cleaned) == 0 {
		r.markFailed(ctx, j.id, "empty_after_cleanup")
		return
	}
	if err := r.replaceKeywords(ctx, j.fcID, cleaned); err != nil {
		r.markFailed(ctx, j.id, "store:"+err.Error())
		return
	}
	r.markDone(ctx, j.id)
}

// loadFlashcard reads the title/question/answer for the prompt input.
func (r *Runner) loadFlashcard(ctx context.Context, fcID int64) (*aipipeline.ExtractInput, error) {
	var in aipipeline.ExtractInput
	err := r.db.QueryRow(ctx,
		`SELECT title, question, answer FROM flashcards WHERE id=$1`, fcID,
	).Scan(&in.Title, &in.Question, &in.Answer)
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// replaceKeywords swaps the keyword set for a flashcard atomically.
func (r *Runner) replaceKeywords(ctx context.Context, fcID int64, kws []aipipeline.ExtractedKeyword) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin:\n%w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sqlReplaceKeywordsDelete, fcID); err != nil {
		return fmt.Errorf("delete keywords:\n%w", err)
	}
	for _, k := range kws {
		if _, err := tx.Exec(ctx, sqlReplaceKeywordsInsert, fcID, k.Keyword, k.Weight); err != nil {
			return fmt.Errorf("insert keyword:\n%w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Runner) markDone(ctx context.Context, id int64) {
	if _, err := r.db.Exec(ctx, sqlMarkDone, id); err != nil {
		log.Printf("keywordWorker: markDone job %d: %v", id, err)
	}
}

func (r *Runner) markFailed(ctx context.Context, id int64, reason string) {
	if _, err := r.db.Exec(ctx, sqlMarkFailed, id, reason); err != nil {
		log.Printf("keywordWorker: markFailed job %d: %v", id, err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/keywordWorker/ -count=1`
Expected: PASS (all of postprocess + enqueue + runner tests).

- [ ] **Step 6: Commit**

```bash
git add internal/keywordWorker/runner.go internal/keywordWorker/runner_test.go pkg/aipipeline/service.go
git commit -m "$(cat <<'EOF'
Add per-job keyword extraction runner

[+] Runner.RunOnce claims one job and processes it
[+] FOR UPDATE SKIP LOCKED claim path
[+] transactional keyword replace (delete-then-insert)
[+] graceful FC-deleted handling (mark done)
[+] markDone / markFailed helpers
[+] aipipeline.NewServiceForTest constructor for integration tests
[+] integration tests for happy path + empty_after_cleanup
EOF
)"
```

---

## Task 11: Implement the rate-limited Worker (poller + goroutine pool)

**Files:**
- Modify: `internal/keywordWorker/worker.go` (replace stub)
- Run: `go get golang.org/x/time/rate`

- [ ] **Step 1: Add the rate-limit dep**

Run:
```bash
go get golang.org/x/time/rate
go mod tidy
```

- [ ] **Step 2: Replace the stub**

Overwrite `internal/keywordWorker/worker.go`:

```go
package keywordWorker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"studbud/backend/pkg/aipipeline"
)

// Config tunes the worker. All fields have safe defaults if zero.
type Config struct {
	Workers      int           // Workers is the number of concurrent runner goroutines (default 2)
	RatePerMin   int           // RatePerMin is the global API call cap (default 60)
	Burst        int           // Burst is the rate-limiter burst size (default 120)
	PollInterval time.Duration // PollInterval is the idle backoff (default 500ms → 5s)
}

// Worker polls ai_extraction_jobs and runs keyword extraction.
type Worker struct {
	cfg     Config
	db      *pgxpool.Pool
	ai      *aipipeline.Service
	limiter *rate.Limiter
	runner  *Runner

	stop chan struct{}
	wg   sync.WaitGroup
}

// New constructs a Worker. Use Start to begin polling.
func New(db *pgxpool.Pool, ai *aipipeline.Service, cfg Config) *Worker {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.RatePerMin <= 0 {
		cfg.RatePerMin = 60
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 120
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	limiter := rate.NewLimiter(rate.Limit(float64(cfg.RatePerMin)/60.0), cfg.Burst)
	return &Worker{
		cfg:     cfg,
		db:      db,
		ai:      ai,
		limiter: limiter,
		runner:  NewRunner(db, ai),
		stop:    make(chan struct{}),
	}
}

// Start launches the poller and N runner goroutines. Non-blocking.
func (w *Worker) Start(ctx context.Context) {
	jobs := make(chan claimedJob, w.cfg.Workers)
	w.wg.Add(1)
	go w.poll(ctx, jobs)
	for i := 0; i < w.cfg.Workers; i++ {
		w.wg.Add(1)
		go w.consume(ctx, jobs)
	}
	log.Printf("keywordWorker: started (workers=%d, rate=%d/min, burst=%d)",
		w.cfg.Workers, w.cfg.RatePerMin, w.cfg.Burst)
}

// Stop signals the worker to exit and waits for goroutines to drain.
func (w *Worker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

// poll claims pending jobs and pushes them to the workers channel.
func (w *Worker) poll(ctx context.Context, out chan<- claimedJob) {
	defer w.wg.Done()
	defer close(out)
	delay := w.cfg.PollInterval
	maxDelay := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		default:
		}
		jobs, err := w.runner.claim(ctx, w.cfg.Workers)
		if err != nil {
			log.Printf("keywordWorker: claim error: %v", err)
			time.Sleep(delay)
			continue
		}
		if len(jobs) == 0 {
			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
			continue
		}
		delay = w.cfg.PollInterval
		for _, j := range jobs {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case out <- j:
			}
		}
	}
}

// consume runs claimed jobs subject to the rate limiter.
func (w *Worker) consume(ctx context.Context, in <-chan claimedJob) {
	defer w.wg.Done()
	for j := range in {
		if err := w.limiter.Wait(ctx); err != nil {
			log.Printf("keywordWorker: rate-limit wait cancelled for job %d: %v", j.id, err)
			return
		}
		w.runner.run(ctx, j)
	}
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: clean exit.

- [ ] **Step 4: Add a worker concurrency smoke test**

Append to `internal/keywordWorker/runner_test.go`:

```go
func TestWorker_ProcessesMultipleJobs(t *testing.T) {
	pool := testutil.NewPool(t)
	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.CreateSubject(t, pool, uid, "Bio")
	const N = 5
	fcs := make([]int64, N)
	for i := 0; i < N; i++ {
		fcs[i] = testutil.CreateFlashcard(t, pool, subj, "T", "Q", "A")
	}
	enq := NewEnqueuer(pool)
	for _, id := range fcs {
		if err := enq.EnqueueForFlashcard(context.Background(), id, PriorityUser); err != nil {
			t.Fatal(err)
		}
	}
	prov := &fakeProv{body: `{"keywords":[{"keyword":"x","weight":0.5}]}`}
	ai := aipipeline.NewServiceForTest(pool, prov, "claude-test")
	w := New(pool, ai, Config{Workers: 2, RatePerMin: 6000, Burst: 100, PollInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var done int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM ai_extraction_jobs WHERE state='done' AND fc_id = ANY($1)`, fcs).Scan(&done)
		if done == N {
			cancel()
			w.Stop()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("worker did not process %d jobs in time", N)
}
```

Add `"time"` to the imports of `runner_test.go` if not already there.

- [ ] **Step 5: Run all worker tests**

Run: `go test ./internal/keywordWorker/ -count=1 -timeout 30s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/keywordWorker/worker.go internal/keywordWorker/runner_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
Replace keyword worker stub with real implementation

[+] Config (Workers, RatePerMin, Burst, PollInterval) with defaults
[+] poll loop with FOR UPDATE SKIP LOCKED claim and exponential backoff
[+] N consumer goroutines paced by golang.org/x/time/rate
[+] Stop() drains in-flight goroutines
[+] integration test for multi-job processing
[+] dep: golang.org/x/time/rate
EOF
)"
```

---

## Task 12: Wire the worker into `cmd/app/deps.go` and main

**Files:**
- Modify: `cmd/app/deps.go:122-131` (`buildInfra` — construct real Worker)
- Modify: `cmd/app/deps.go:152-169` (`buildDomainServices` — pass enqueuer to flashcard)
- Modify: `cmd/app/main.go` (start worker, register reaper, call `MaterialChange` from `flashcard`'s `shouldReindex` var)

- [ ] **Step 1: Pass the AI service into infra**

`buildInfra` currently builds the worker before the aipipeline service exists. Restructure: build the worker AFTER the aipipeline service is built. Easiest path: move worker construction out of `buildInfra` into a new `wireWorker` step run after `buildStubServices`.

Edit `cmd/app/deps.go` `buildInfra` to drop the worker field temporarily — return `infra` with `worker: nil`:

```go
return infra{
	signer:    jwtsigner.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTL),
	store:     store,
	emailer:   buildEmailer(cfg),
	scheduler: cron.New(),
	worker:    nil, // wired after aipipeline service exists
	aiClient:  selectAIClient(cfg),
	billing:   billingadapter.NoopClient{},
	hub:       duelHub.New(),
}, nil
```

Then in `buildDeps`, after `stubs := buildStubServices(...)` and before `assembleDeps`:

```go
inf.worker = keywordWorker.New(pool, stubs.ai, keywordWorker.Config{
	Workers:    cfg.KeywordWorkers,
	RatePerMin: cfg.KeywordRatePerMin,
	Burst:      cfg.KeywordBurst,
})
enqueuer := keywordWorker.NewEnqueuer(pool)
flashcard.SetReindexPredicate(keywordWorker.MaterialChange)
dom.flashcard = flashcard.NewService(pool, dom.access, enqueuerAdapter{enqueuer})
```

This requires three small additions:

a) `enqueuerAdapter` adapts `*keywordWorker.Enqueuer` (which takes `Priority`) to the `flashcard.KeywordEnqueuer` interface (which takes `int16`):

```go
type enqueuerAdapter struct{ inner *keywordWorker.Enqueuer }

func (a enqueuerAdapter) EnqueueForFlashcard(ctx context.Context, fcID int64, prio int16) error {
	return a.inner.EnqueueForFlashcard(ctx, fcID, keywordWorker.Priority(prio))
}
```

Add this near the bottom of `cmd/app/deps.go`. Add the `context` import.

b) `flashcard.SetReindexPredicate` — add a small exported setter to `pkg/flashcard/service.go`:

```go
// SetReindexPredicate replaces the package-level shouldReindex var. Wired from
// cmd/app/deps.go to point at internal/keywordWorker.MaterialChange.
func SetReindexPredicate(fn func(oldQ, oldA, newQ, newA string) bool) {
	if fn != nil {
		shouldReindex = fn
	}
}
```

c) Three new env-tunable config fields. Append to `internal/config/config.go` `Config` struct:

```go
KeywordWorkers    int    `env:"KEYWORD_EXTRACT_WORKERS" envDefault:"2"`
KeywordRatePerMin int    `env:"KEYWORD_EXTRACT_RATE_PER_MIN" envDefault:"60"`
KeywordBurst      int    `env:"KEYWORD_EXTRACT_BURST" envDefault:"120"`
```

(Match the env-tag style of the existing fields in `config.go`. If the project uses a different env library, mirror that.)

- [ ] **Step 2: Start the worker in main**

In `cmd/app/main.go`, after `deps` is built and before `http.ListenAndServe`, add:

```go
deps.worker.Start(ctx)
defer deps.worker.Stop()
```

(Use the existing context that the server uses, however it's plumbed.)

- [ ] **Step 3: Register the stuck-job reaper as a cron job**

If `cmd/app` already wires cron jobs (the survey said `aipipeline.ReapOrphanedJobs` is registered somewhere), find that registration and add a sibling for the extraction reaper. If not yet present, register both at boot.

In whichever file calls `scheduler.Register(...)`, add:

```go
deps.scheduler.Register("reap-extraction-jobs", time.Minute, func(ctx context.Context) error {
	tag, err := deps.db.Exec(ctx, "UPDATE ai_extraction_jobs SET state='pending', attempts=attempts+1, last_error='reaped: running > 5m', updated_at=now(), started_at=NULL WHERE state='running' AND started_at < now() - interval '5 minutes'")
	if err != nil {
		return fmt.Errorf("reap extraction jobs:\n%w", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Printf("keywordWorker: reaped %d stuck running jobs", n)
	}
	return nil
})
```

Adjust to match `cron.Scheduler`'s actual API surface (the survey didn't sample its signature; use the existing `aipipeline` reaper registration as a template).

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: clean exit.

- [ ] **Step 5: Run unit + integration tests for cmd/app**

Run: `go test ./cmd/app/... ./pkg/flashcard/... ./internal/keywordWorker/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/app/deps.go cmd/app/main.go pkg/flashcard/service.go internal/config/config.go
git commit -m "$(cat <<'EOF'
Wire keyword worker into app boot

[+] keywordWorker.Worker constructed after aipipeline service exists
[+] enqueuerAdapter bridges Enqueuer to flashcard.KeywordEnqueuer
[+] flashcard.SetReindexPredicate wires MaterialChange at startup
[+] config fields KEYWORD_EXTRACT_WORKERS/RATE_PER_MIN/BURST
[+] worker.Start at boot, Stop on shutdown
[+] cron-registered reaper resets jobs running > 5 min
EOF
)"
```

---

## Task 13: One-shot backfill on boot

**Files:**
- Modify: `db_sql/setup_ai.go` (append backfill SQL after the `flashcard_keywords` index)

The cleanest place is the same schema setup, which runs idempotently at every boot. The `ON CONFLICT DO NOTHING` ensures re-runs add no duplicates.

- [ ] **Step 1: Append the backfill statement**

In `db_sql/setup_ai.go`, append at the end of the `aiSchema` const (after the last `CREATE INDEX`):

```sql
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
SELECT id, -1, 'pending' FROM flashcards
ON CONFLICT (fc_id) WHERE state IN ('pending','running') DO NOTHING;
```

- [ ] **Step 2: Restart the dev DB and verify**

Reset the DB so the schema runs fresh (or just restart the server — the statement is idempotent). Then:

```bash
psql -d studbud_dev -c "SELECT count(*) FROM ai_extraction_jobs WHERE priority=-1"
```

If you have any pre-existing flashcards, you should see a positive count.

- [ ] **Step 3: Commit**

```bash
git add db_sql/setup_ai.go
git commit -m "$(cat <<'EOF'
Backfill keyword-extraction jobs on schema setup

[+] one-shot enqueue at priority -1 for all existing flashcards
[+] ON CONFLICT DO NOTHING keeps re-runs idempotent
EOF
)"
```

---

## Task 14: End-to-end smoke check

**Files:**
- None (manual verification with the running server)

This is a pre-merge ritual — not automated.

- [ ] **Step 1: Start the server with `KEYWORD_EXTRACT_RATE_PER_MIN=10` and `KEYWORD_EXTRACT_WORKERS=1`** so the worker is observable.

```bash
KEYWORD_EXTRACT_RATE_PER_MIN=10 KEYWORD_EXTRACT_WORKERS=1 ./launch_app.sh
```

- [ ] **Step 2: Create a flashcard via the API.** Watch logs for the enqueue + run + done sequence.

```bash
curl -X POST http://localhost:8080/flashcard-create -H "Authorization: Bearer $TOKEN" -d '{"subject_id":1,"chapter_id":null,"title":"Mitose","question":"Quelles sont les phases de la mitose ?","answer":"Prophase, métaphase, anaphase, télophase."}'
```

- [ ] **Step 3: Check the keyword rows landed.**

```bash
psql -d studbud_dev -c "SELECT keyword, weight FROM flashcard_keywords WHERE fc_id = (SELECT max(id) FROM flashcards) ORDER BY weight DESC"
```

Expected: 5–12 rows of normalized French keywords with weights in 0..1.

- [ ] **Step 4: Edit a typo on the same card via `/flashcard-update`** — confirm NO new job (MaterialChange filters it).

- [ ] **Step 5: Edit a paragraph rewrite** — confirm a new job appears in `ai_extraction_jobs` and runs.

- [ ] **Step 6: Stop the server mid-job** (Ctrl+C). Confirm no orphan `running` rows after 5 minutes (the reaper resets them on the next boot).

If anything misbehaves, fix and re-test before declaring done. No commit for this task.

---

## Self-Review Notes

- **Spec coverage:** every Spec B.0 section maps to a task. §4.1 schema → Task 1; §5 AI contract → Tasks 3–5; §6.1 enqueue → Tasks 7–9; §6.3 worker loop → Tasks 10–11; §6.4 rate limiting → Task 11; §7 retry/reaper → Tasks 11–12; §8 backfill → Task 13.
- **Out of scope correctly excluded:** no Prometheus metrics (§9 of spec is "Observability" — only structured logs are present here; metrics defer to a follow-up).
- **Pre-existing `extract_keywords_calls` quota column:** intentionally unused. Spec §2 is explicit ("System-side. Does not debit any user-facing quota counter."). Leave the column in place (no schema churn).
- **One known gap:** the spec mentions transient retry with backoff (5s → 30s → terminal). The current runner marks failed immediately on AI error. That's acceptable for v1 since the worker re-claims on the next poll cycle for any future enqueue, but `markFailed` could be promoted to `sqlRequeueAfterError` (defined in queries.go) once the failure-classification taxonomy is locked. Tracked in the runner's `markFailed` call sites — easy follow-up.
- **Test scaffolding assumption:** `testutil.CreateSubject` and `testutil.CreateFlashcard` were assumed but not verified against the real `testutil` API. If they don't exist, swap to direct `INSERT` SQL in the tests — the structure of each test stays valid.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-30-flashcard-keyword-extraction.md`.** This is the prerequisite for Spec B; once the worker ships and is observed in dev, I'll write Plan B (revision plan) on top of a confirmed-live keyword index.

**Two execution options:**

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
