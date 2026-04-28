# Flashcard generation: coverage + auto-chapters on prompt flow

## Summary

Bring the prompt-based flashcard generation flow (`POST /ai/flashcards/prompt`) closer in capability to the PDF flow by adding a `coverage` knob and an `auto_chapters` toggle, and remove the obsolete `target_count` parameter. Rename the existing PDF `coverage` enum values for consistency across both flows. Both flows share a single, richer coverage taxonomy: **Core / Balanced / Comprehensive**.

The `style` field is untouched.

## Motivation

Today, the prompt flow only exposes `style` (per-card verbosity). Users have no way to tell the model "stick to definitions and theorems" vs "include propositions, lemmas" vs "include examples and remarks too" — that's a coverage decision, orthogonal to per-card verbosity. The PDF flow already has `coverage` for exactly this reason; the prompt flow should too.

The PDF flow's existing values (`essentials` / `balanced` / `comprehensive`) read as ad-hoc and don't match the new vocabulary. Renaming to `Core` / `Balanced` / `Comprehensive` aligns both flows on a single enum.

`target_count` (number of cards) on the prompt flow goes away: the model decides based on coverage and the user's prompt. Forcing a count produces filler or truncation.

Auto-chapters is missing on the prompt flow but useful for any sufficiently broad prompt — same tri-state semantics as PDF flow.

## API changes

### `POST /ai/flashcards/prompt`

Request body changes:

| Field | Before | After |
|---|---|---|
| `subject_id` | int64 (required) | unchanged |
| `chapter_id` | int64 (optional) | unchanged |
| `prompt` | string (required, ≤8000) | unchanged |
| `style` | "short" \| "standard" \| "detailed" | unchanged |
| `focus` | string (optional) | unchanged |
| `target_count` | int (0 = auto, 1..50) | **removed** |
| `coverage` | (none) | **added** — "Core" \| "Balanced" \| "Comprehensive", default "Balanced" |
| `auto_chapters` | (none) | **added** — bool, default false |

Behavior:
- `coverage` defaults to `Balanced` when omitted or empty.
- `auto_chapters` is a tri-state in combination with `chapter_id`:
  1. `chapter_id` set → ignore `auto_chapters`; cards land in the named chapter, no `chapters` array in output.
  2. `chapter_id` unset, `auto_chapters` true → model proposes chapters; output contains a `chapters` array and each card carries `chapterIndex`.
  3. `chapter_id` unset, `auto_chapters` false → no chapter structure; cards omit `chapterIndex` (or it's null).
- The output schema for the prompt flow becomes the same shape as the PDF flow's: optional `chapters` array, items carry optional `chapterIndex`.

Validation:
- `coverage` must be one of the three enum values when provided. Reject other values with `400 validation`.
- Invalid combinations (e.g., `coverage` typo) follow the existing `myErrors.ErrValidation` path.

### `POST /ai/flashcards/pdf`

Request body change: `coverage` enum values rename only.

| Before | After |
|---|---|
| `essentials` | `Core` |
| `balanced` | `Balanced` |
| `comprehensive` | `Comprehensive` |

Default remains the middle value (`Balanced`). No structural changes to the PDF flow — same fields, same semantics, same output schema.

### OpenAPI

`api/handler/docs_openapi.yaml` updated to reflect:
- Prompt-flow request schema: drop `target_count`, add `coverage` and `auto_chapters`, document the chapter tri-state.
- Prompt-flow response schema: include the optional `chapters` array and the optional per-card `chapterIndex` (matching PDF flow).
- PDF-flow `coverage`: enum values renamed.

## Internal changes

### `pkg/aipipeline/prompts.go`

- `PromptGenValues`:
  - Remove `Target int`.
  - Add `Coverage string` ("Core" | "Balanced" | "Comprehensive").
  - Add `AutoChapters bool`.
- `PDFGenValues`:
  - `Coverage` doc comment updated to the new enum values.
  - **Remove** `CoverageHint string` from the public struct — it duplicates state with `Coverage` and forces the handler to compute it.

The hint string is computed internally by `RenderPromptGen` / `RenderPDFGen` from `Coverage` via a private helper `coverageHint(c string) string` in `pkg/aipipeline`. Templates reference a derived value (e.g., `{{.Coverage}}` plus a `{{template-helper}}` or pre-computed `.CoverageHint` injected by the renderer wrapper before `Execute` — see below).

Concrete approach (KISS): the public `PromptGenValues` / `PDFGenValues` carry only `Coverage`. Inside `renderPromptGenPrompt` / `renderPromptGenPDF`, build a small private struct (e.g., `pdfTemplateData{ PDFGenValues; Hint string }`) with the hint filled in, and pass that to `tmpl.Execute`. Templates reference `{{.Hint}}`. The hint helper stays unexported — handlers don't need it.

Hint mapping:

- `Core` → "cover only the core notions: the definitions and core theorems / facts that are unavoidable to understand the subject"
- `Balanced` → "cover the core notions plus secondary results — propositions, lemmas, named methods — that build on the core"
- `Comprehensive` → "cover everything substantive, including examples, remarks, edge cases, and connections between topics"

The hint text is intentionally subject-agnostic (uses "results", "named methods" rather than "theorem", "lemma" exclusively — math vocabulary is illustrative, not literal). The model receives both the level name and the hint.

### Prompt template `pkg/aipipeline/prompts/generate_prompt.tmpl`

Changes:
- Drop the `{{.Target}}` reference and the "Generate {{.Target}} flashcards" sentence — replace with a coverage-driven instruction (the model decides the count).
- Add a coverage line: `Coverage: {{.Coverage}} ({{.Hint}}).` (hint comes from the internal renderer wrapper described above).
- Add the auto-chapters block: same conditional `{{if .AutoChapters}}…{{end}}` wording the PDF template uses, adjusted for prompt context (no "PDF" reference).
- Output JSON description updated: now includes optional `chapters` and per-card `chapterIndex`, matching the PDF template's shape.

### Prompt template `pkg/aipipeline/prompts/generate_pdf.tmpl`

- Reference `{{.Hint}}` instead of `{{.CoverageHint}}` (since `CoverageHint` is dropped from the public struct).
- No other text changes. The hint text *will* change because `coverageHint` now returns the new wording.

### Schema for the prompt flow

`api/handler/ai.go::defaultItemsSchema` is replaced by the same shape as `defaultPDFItemsSchema` (chapters array + chapterIndex on items). Both flows can share one helper, e.g., `defaultGenItemsSchema()` in `ai.go`, called from both handlers — small dedup that's worth doing while we're here.

### Handlers

`api/handler/ai.go`:
- `promptGenInput` struct: remove `TargetCount`, add `Coverage` and `AutoChapters`.
- `decodePromptGen`:
  - Drop the target-count handling.
  - Default `coverage` to `"Balanced"` when empty.
  - Validate `coverage` against the three allowed values.
- `renderPromptGenPromptExported` passes the new fields through to `PromptGenValues` (with `AutoChapters && in.ChapterID == 0` so the suppression rule lives in the handler, same as PDF flow). No hint computation in the handler — that's the renderer's job now.
- `GenerateFromPrompt`: update the metadata map — drop `target_count`, add `coverage` and `auto_chapters`.
- Replace `defaultItemsSchema()` with `defaultGenItemsSchema()` (the chapters-aware version), shared with PDF flow.

`api/handler/ai_pdf.go`:
- Update the default value passed to `orDefaultStr(r.FormValue("coverage"), "Balanced")`.
- **Remove** the local `coverageHint` function entirely — the renderer in `pkg/aipipeline` now owns hint computation.
- `renderPDFPrompt` no longer sets `CoverageHint` (struct field is gone).
- No other changes.

## Tests

- `pkg/aipipeline/prompts_test.go` — update prompt-template golden cases to reflect:
  - No `Target` substitution in prompt template.
  - Coverage line present in prompt template.
  - Auto-chapters block conditionally rendered.
  - PDF template's coverage-hint output reflects new wording.
- `cmd/app/e2e_ai_test.go` — adjust any prompt-flow request bodies that use `target_count`; replace with `coverage` and (where applicable) `auto_chapters`. Adjust PDF tests if they pin specific `coverage` values to the old names.
- `api/handler/ai_generate_test.go` — same: update request bodies, add validation tests for the new `coverage` enum (good value, bad value, missing value defaults).
- Confirm e2e tests for the PDF flow still pass after the rename — search for hard-coded `"essentials"` / `"balanced"` / `"comprehensive"` strings in tests.

## Migration / compatibility

This is a backwards-incompatible API change for both flows:
- Prompt flow: `target_count` removed; coverage required-ish (defaults applied, but unknown values rejected).
- PDF flow: old enum values rejected.

The product is pre-launch (per project convention so far — no public API consumers in tree); we cut over without aliases. If a frontend exists in another repo it will need a one-line update.

## Out of scope

- Persisting `coverage` / `auto_chapters` defaults on the user or subject record.
- Changing `style` semantics or values.
- Streaming/SSE wire format.
- The check or commit endpoints.
