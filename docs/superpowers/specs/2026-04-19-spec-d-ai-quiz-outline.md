# Spec D — AI Quiz Generation (Outline) — SUPERSEDED

**Status:** Superseded by full spec [`2026-04-21-ai-quiz-design.md`](2026-04-21-ai-quiz-design.md).
**Date:** 2026-04-19
**Scope:** A new study mode where the app generates on-demand quiz questions (multi-choice, fill-in-blank, true/false) from the user's existing flashcards. Distinct from the swipe-based training flow already shipped. Uses Spec A's AI pipeline primitive. AI subscribers only.

Not in scope: shared quizzes (friends competing → Spec E), public quiz marketplaces, non-AI quiz builders, test/exam invigilation modes.

---

## Purpose

Existing training (swipe + self-graded flip) is passive — users rate themselves. A quiz mode adds **active recall with objective scoring**: the app generates a question, the user picks/types an answer, the app scores it. This sharpens retention, adds a second study modality for variety, and sets up ground truth for automatic difficulty estimation later.

Quizzes run against the user's own flashcards. The AI generates the questions, distractors, and scoring rubric from the source card; the card's "answer" becomes the correct answer, and the AI fabricates plausible wrong options.

## Dependencies

- **Spec A (shipped)** — `RunStructuredGeneration` pipeline, entitlement, per-feature daily quotas. Adds a `quiz` counter.
- **Spec B (optional)** — if we want "today's quiz" to appear on the AI-mode home as part of the daily plan, it integrates with the revision plan. Otherwise standalone.

## Known Decisions (carved out from prior specs)

- All AI calls route through `RunStructuredGeneration` with a new `FeatureGenerateQuiz` feature key. (Spec A §3.2)
- Per-feature daily quota — add `quiz` to the existing `prompt` / `pdf` / `check` / `plan` set. (Spec A §2)
- AI subscribers only, gated by `ai_subscription_active`. (Spec A §2)
- No provider keys on client. (Spec A §3.2)

## Key Open Questions (resolve during brainstorming)

### Product
1. **Question-type mix** — multi-choice only? Include fill-in-blank, true/false, short-answer? Let user pick types per quiz?
2. **Quiz size** — fixed (e.g. 10 questions) or user-chosen (5 / 10 / 20 / all)?
3. **Card selection** — all cards in a subject/chapter? Only "Bad/OK" cards for remediation? Cards due per `dueHeuristic`? Let user filter?
4. **Timed mode** — optional per-question timer? Total quiz timer?
5. **Scoring** — binary (right/wrong) or partial credit for near-misses (especially fill-in-blank)?
6. **Results screen** — per-question review with explanation + original flashcard? Mastery update based on quiz result?
7. **Integration with training state** — does a correct quiz answer count as "Good" for the card's `lastResult`? Wrong answer as "Bad"? Or keep them separate?
8. **Reusable quizzes** — save a quiz for later, or always regenerate? Leaderboard per quiz (would imply persistence)?
9. **Per-day quiz in AI mode home** — alongside the plan card, or replacing part of it?
10. **Public / sharing** — deferred entirely, or could a subject owner share a quiz with collaborators?

### Technical
11. **Question quality guardrails** — how do we detect/reject nonsense distractors? Sample-and-review, or trust the model?
12. **Cheating resistance** — user can see the answer in their flashcard editor. Not a real concern (self-study), but the UX shouldn't accidentally display it during the quiz.
13. **Deterministic seeding** — should regenerating a quiz produce the same questions (seeded by subject + date)? Users might want consistency for retakes.
14. **Persistence** — quizzes stored, or transient? If stored, when pruned?
15. **Quota accounting** — 1 quiz generation = 1 unit regardless of size? Or scale with size?
16. **Multilingual** — respect the flashcard's language when generating distractors. Same prompt handles all, or language-specific templates?

## Architectural Sketch (non-binding)

```
User taps "Start Quiz" on Subject/Chapter
  ├─▶ QuizSetupPage: pick size, types, card pool
  │
  ├─▶ POST /quiz/generate { subjectId, chapterId?, size, types, filter }
  │     ├─▶ aiQuotaService.Debit(user, "quiz", 1)
  │     ├─▶ select candidate flashcards from DB
  │     ├─▶ RunStructuredGeneration(FeatureGenerateQuiz, ...)
  │     └─▶ persist Quiz + Questions → return quizId
  │
  ├─▶ QuizPlayPage: one question at a time, submit answer
  │     └─▶ POST /quiz/:id/answer { questionId, answer } → { correct, explanation? }
  │
  └─▶ QuizResultsPage: score, per-question review, "Retake"/"Review cards"
        └─▶ optional: POST /update-flashcard-result × N based on quiz outcomes
```

### New modules (tentative)
- `pkg/ai/` — add `FeatureGenerateQuiz` + `prompts/generate_quiz.txt`.
- `api/service/quizService.go` — orchestration.
- `api/handler/quizHandler.go` — `POST /quiz/generate`, `GET /quiz/:id`, `POST /quiz/:id/answer`, `GET /quiz/:id/results`.
- Schema: `quizzes(id, user_id, subject_id, chapter_id, settings, created_at, completed_at)` + `quiz_questions(id, quiz_id, fc_id, question_text, question_type, options_jsonb, correct_index_or_text, explanation)` + `quiz_answers(quiz_id, question_id, user_answer, correct, answered_at)`.

### Frontend
- `pages/QuizSetupPage.vue`, `QuizPlayPage.vue`, `QuizResultsPage.vue`.
- `components/quiz/MultiChoiceQuestion.vue`, `FillBlankQuestion.vue`, `TrueFalseQuestion.vue`.
- Quiz entry points: button on Subject Detail + Chapter Detail, alongside "Start Training".

### AI contract sketch
Input (per card to question-ify):
```json
{ "title": "...", "question": "...", "answer": "...", "targetType": "multi_choice" }
```

Output (per question):
```json
{
  "questionType": "multi_choice",
  "stem": "...",
  "options": ["...", "...", "...", "..."],
  "correctIndex": 2,
  "explanation": "..."
}
```

Pipeline generates one question per selected card, in a single AI call (array in / array out) for efficiency.

## Risks

- **Hallucination in distractors** — the AI might generate distractors that are accidentally correct. Mitigation: prompt engineering + optional user-flag button per question (feeds back as telemetry).
- **Card length mismatch** — flashcards with very long answers make bad quiz fodder (especially multi-choice). Need heuristics to filter or truncate source card.
- **Quota stacking** — user generates a quiz, then regenerates 5× searching for "the right questions." Hard-cap quota makes this self-limiting but UX should surface "X quizzes left today."
- **Offline behavior** — if the user starts a quiz online and loses connection mid-quiz, submit-answer calls fail. Quiz state should be resumable.

## Testing Strategy (high-level)

- Unit: question-type generators, answer scoring (especially fuzzy-match for fill-in-blank).
- Integration: full generate → play → score flow against mocked AI.
- Prompt regression: golden quiz fixtures per card type (short, long, math, code).

## Next Step Before Full Spec

Brainstorming session to resolve question mix, size, scoring model, mastery-integration, and the "standalone vs plan-integrated" question. These are product-shape decisions; the architecture is largely dictated by Spec A once they're set.
