# Spec D — AI Quiz Generation (Design)

**Status:** Approved — ready for implementation planning.
**Date:** 2026-04-21
**Supersedes:** [`2026-04-19-spec-d-ai-quiz-outline.md`](2026-04-19-spec-d-ai-quiz-outline.md)
**Scope:** A new study mode that generates objective, on-demand quizzes from the user's flashcards. Distinct from the swipe-based training flow. Two discrete quiz kinds (`specific` cards vs `global` knowledge). Integrates into Spec B revision plans. AI subscribers only (plus a one-time free demo per non-subscriber). Shareable to friends.

Not in scope: public quiz marketplace, invigilated exam mode, asynchronous quiz tournaments (see Spec E for competitive multiplayer), reshare of shared-copy quizzes.

---

## 1. Architecture Overview

### High-level flow

```
Two entry points:
 ┌────────────────────────────────┐      ┌─────────────────────────────┐
 │ User-initiated (QuizSetupPage) │      │ Plan-initiated (TodayPlan)  │
 │ - picks kind, size, types      │      │ - AI pre-picked kind/size    │
 │ - debits aiQuota.quiz          │      │ - quota already paid at plan │
 └─────────────┬──────────────────┘      └──────────────┬──────────────┘
               │                                        │
               └────────────────┬───────────────────────┘
                                ▼
                    POST /quizzes/generate
                                │
                                ▼
        ┌────────────────────────────────────────────┐
        │ RunStructuredGeneration(FeatureGenerateQuiz)│   (Spec A pipeline)
        │  - entitlement check                        │
        │  - optional quota debit                     │
        │  - provider call                            │
        └────────────────┬───────────────────────────┘
                         │
                         ▼
                ┌─────────────────┐
                │ quizService     │
                │  - persists     │
                │    quiz + Qs    │
                │  - returns id   │
                └────────┬────────┘
                         │
                         ▼
               User plays (linear,
               commit-on-answer,
               resumable)
                         │
                         ▼
               Results → Retake / Share / Review cards
```

### Backend module map
- `pkg/ai/` — add `FeatureGenerateQuiz` + `prompts/generate_quiz.txt` (Spec A contract).
- `api/service/quizService.go` — orchestration: generate, start attempt, answer, complete, retake, share, accept.
- `api/handler/quizHandler.go` — endpoints listed in §3.
- `api/service/revisionPlanService.go` — extended to emit `quizSlots` in day-plan JSON (driven by `intensity`).
- `pkg/plan/` — existing day-plan types gain `QuizSlot` struct.
- `api/service/aiQuotaService.go` — add `quiz` counter; add `quizDemoUsed` per-user flag for non-subscriber free demo.

### Frontend module map
- `stores/quiz.ts` — attempt state, actions wrapping endpoints.
- `pages/QuizSetupPage.vue`, `QuizPlayPage.vue`, `QuizResultsPage.vue`, `QuizSharedPage.vue`.
- `components/quiz/MultiChoiceQuestion.vue`, `TrueFalseQuestion.vue`, `FillBlankQuestion.vue`, `QuizKindBadge.vue`, `QuizResultRow.vue`, `QuizShareDialog.vue`, `QuizSlotRow.vue`.
- `components/gamification/TodayPlanCard.vue` — renders quiz slots alongside training rows.

### Hard boundaries
- **All AI calls** go through `RunStructuredGeneration` (Spec A invariant). No direct provider calls from quiz code.
- **Only `quizService`** writes `quizzes`, `quiz_questions`, `quiz_attempts`, `quiz_attempt_answers`. Revision-plan service reads/writes through `quizService` when materializing slots.
- **Questions are immutable** after insert. Fixing a bad question = regenerate the quiz (creates a new row).
- **Plan-materialized quizzes skip the `quiz` counter** via a `planQuotaCovered` flag propagated through `planContext` — the counter is already debited at plan-generation time.
- **Correctness is never sent to the client before the answer is submitted.** `correct_jsonb` lives server-side only.
- **Shared-copy quizzes are independent rows** owned by the recipient — revoking the share link does not affect clones already taken.

---

## 2. Data Model

### New tables

**`quizzes`** — one row per generated quiz (the pool + metadata).
```sql
CREATE TABLE quizzes (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    chapter_id BIGINT REFERENCES chapters(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('specific','global')),
    source TEXT NOT NULL CHECK (source IN ('user','plan','shared_copy')),
    source_plan_id BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    source_share_token TEXT,                      -- if source='shared_copy'
    card_pool_jsonb JSONB NOT NULL,               -- [fc_id,...] snapshot at generation
    settings_jsonb JSONB NOT NULL,                -- { size, types[], difficulty, timed }
    question_count INT NOT NULL,
    model TEXT NOT NULL,
    prompt_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON quizzes (user_id, created_at DESC);
CREATE INDEX ON quizzes (subject_id);
```

**`quiz_questions`** — immutable, snapshot of what the AI generated.
```sql
CREATE TABLE quiz_questions (
    id BIGSERIAL PRIMARY KEY,
    quiz_id BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    ordinal INT NOT NULL,
    question_type TEXT NOT NULL CHECK (question_type IN ('multi_choice','true_false','fill_blank')),
    stem TEXT NOT NULL,
    options_jsonb JSONB,                          -- MCQ options; null for fill_blank
    correct_jsonb JSONB NOT NULL,                 -- {index:2} | {value:true} | {accepted:["..."]}
    explanation TEXT,
    referenced_fc_ids_jsonb JSONB NOT NULL,       -- [fc_id,...] cards this Q draws from
    UNIQUE (quiz_id, ordinal)
);
```

**`quiz_attempts`** — one per retake; linear play state.
```sql
CREATE TABLE quiz_attempts (
    id BIGSERIAL PRIMARY KEY,
    quiz_id BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed','abandoned')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    correct_count INT NOT NULL DEFAULT 0,
    total_count INT NOT NULL,
    score_pct INT,                                -- 0..100, NULL until completed
    plan_id BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    plan_date DATE
);
CREATE INDEX ON quiz_attempts (user_id, started_at DESC);
CREATE INDEX ON quiz_attempts (quiz_id, state);
CREATE UNIQUE INDEX ON quiz_attempts (quiz_id, user_id) WHERE state = 'in_progress';
```

**`quiz_attempt_answers`** — commit-on-answer; drives resume.
```sql
CREATE TABLE quiz_attempt_answers (
    attempt_id BIGINT NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_answer_jsonb JSONB NOT NULL,
    correct BOOLEAN NOT NULL,
    answered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (attempt_id, question_id)
);
```

**`quiz_share_links`** — shareable quiz tokens.
```sql
CREATE TABLE quiz_share_links (
    token TEXT PRIMARY KEY,                       -- 64-char hex
    quiz_id BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,                       -- NULL = never
    revoked_at TIMESTAMPTZ
);
CREATE INDEX ON quiz_share_links (quiz_id);
```

**`quiz_sent_to_friends`** — dedup + "already sent" UI state.
```sql
CREATE TABLE quiz_sent_to_friends (
    quiz_id BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);
```

**`quiz_quality_reports`** — user flags a bad question.
```sql
CREATE TABLE quiz_quality_reports (
    id BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK (reason IN ('wrong_answer','bad_distractors','unclear','off_topic','other')),
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON quiz_quality_reports (question_id);
```

### Amendments to existing tables

**`revision_plans`** — gains `intensity` and extends `days_jsonb` shape.
```sql
ALTER TABLE revision_plans
    ADD COLUMN intensity TEXT NOT NULL DEFAULT 'normal'
    CHECK (intensity IN ('light','normal','intense'));
```

Day-plan JSONB extended (non-breaking — existing plans have no `quizSlots`):
```typescript
type DayPlan = {
  date: string,              // YYYY-MM-DD
  trainingCards: number[],   // existing — fc_ids (unchanged)
  quizSlots?: QuizSlot[],    // NEW — generated on demand when the user opens the day
}
type QuizSlot = {
  kind: 'specific' | 'global',
  suggestedSize: number,     // AI-picked 5|10|15|20
  suggestedTypes: QuestionType[],
  cardPool: number[],        // fc_ids the AI selected for this slot
  quizId?: number,           // filled once materialized
}
```

**`ai_quota_state` (Spec A)** — extends existing per-day counter.
```sql
ALTER TABLE ai_quota_state
    ADD COLUMN quiz_demo_used BOOLEAN NOT NULL DEFAULT FALSE;
-- plus a new counter key 'quiz' joining existing 'prompt'|'pdf'|'check'|'plan'
```

### Key invariants
- `quiz_questions` are **immutable** post-insert. Fixing bad questions = regenerate (new row).
- `quiz_attempt_answers.correct` is computed **server-side** and never recomputed — locks score against retroactive changes.
- At most one `in_progress` attempt per `(quiz_id, user_id)` (partial UNIQUE index).
- `source='shared_copy'` quizzes are independent rows owned by the recipient — revoking the original share link does not affect clones already taken.
- Plan-materialized quizzes carry `source='plan'` + `source_plan_id`; completion writes through to `revision_plan_progress` (Spec B).

---

## 3. Backend Endpoints

### Generation
- **`POST /quizzes/generate`** — body `{ subjectId, chapterId?, kind, size, types[], cardFilter?, planContext? }`
  - `kind: 'specific' | 'global'`
  - `cardFilter: 'all' | 'bad_ok' | 'due'` (specific only)
  - `planContext: { planId, planDate, slotIndex }` — skips quota debit when present
  - Debits `aiQuota.quiz` unless `planContext` present
  - Resolves pool: specific → user filter; global → AI coverage pre-selection
  - `RunStructuredGeneration(FeatureGenerateQuiz, ...)`
  - Inserts `quizzes` + `quiz_questions` transactionally
  - Returns `{ quizId, questionCount, kind }`

### Play (linear, commit-on-answer)
- **`POST /quizzes/:id/start`** — idempotent. Returns existing `in_progress` attempt or creates one. Returns `{ attemptId, nextQuestion, progress }`. Never includes `correct_jsonb`.
- **`GET /quizzes/:id/attempts/:attemptId/resume`** — current position + answered questions (without correctness).
- **`POST /quizzes/:id/attempts/:attemptId/answer`** — body `{ questionId, answer }`
  - Server scores by question type
  - Inserts `quiz_attempt_answers`, updates `correct_count`
  - On last question: sets `state='completed'`, writes `score_pct`; if `plan_id` set, writes `revision_plan_progress` rows for each referenced fc_id
  - Returns `{ correct, correctAnswer, explanation, nextQuestion | null }`
- **`POST /quizzes/:id/attempts/:attemptId/abandon`** — sets `state='abandoned'`, frees the unique in-progress slot.

### Retake
- **`POST /quizzes/:id/retake`** — creates a new attempt over same questions (no AI call, no quota). 409 if in-progress attempt exists.

### Results
- **`GET /quizzes/:id/attempts/:attemptId`** — full review payload: per-question stem/options/userAnswer/correctAnswer/explanation/correct + score + referenced flashcards.
- **`GET /quizzes/:id/history`** — all attempts for this quiz by current user.

### Sharing
- **`POST /quizzes/:id/share`** — body `{ expiresAt? }` → `{ token, url }`.
- **`DELETE /quizzes/shares/:token`** — sets `revoked_at` (owner only).
- **`POST /quizzes/:id/send-to-friends`** — body `{ friendIds[], shareToken }` → inserts `quiz_sent_to_friends`, emits notifications.
- **`GET /quizzes/shared/:token`** — auth required, no ownership check. Returns `{ owner, subjectTitle, kind, questionCount, expired }` without questions.
- **`POST /quizzes/shared/:token/accept`** — clones quiz to caller's account (`source='shared_copy'`, copies `quiz_questions` verbatim + `card_pool_jsonb` as frozen ref; does NOT grant subject access). Rejects if expired/revoked or if caller already accepted. Returns new `quizId`.

### Quality
- **`POST /quiz-questions/:id/report`** — body `{ reason, note? }` → inserts `quiz_quality_reports`. Pure telemetry.

### Admin / Dev
- **`POST /admin/reset-quiz-demo`** — dev only; clears `quizDemoUsed` for QA.

All endpoints require verified auth; entitlement via `user_has_ai_access(uid)` (Spec C) except the demo path in `/quizzes/generate` (allowed exactly once per user via `quizDemoUsed`).

---

## 4. Frontend UX

### New pages

**`QuizSetupPage.vue`** — route `/subjects/:sid/quiz/new?chapter=:cid?`
- Segmented control: **Specific cards** | **Global knowledge** (no mixing).
- Size chips: 5 / 10 / 15 / 20.
- Types multiselect: Multi-choice (default on), True/false, Fill-blank.
- Card filter (specific only): All / Bad+OK / Due today.
- Quota badge + "Generate Quiz" CTA.
- Non-subscriber first-visit: inline free-demo banner.

**`QuizPlayPage.vue`** — route `/quizzes/:id/play`
- Calls `/start` on mount; renders one question per screen via type-specific component.
- After submit: correctness + explanation + "Next" (or "Finish" on last).
- No back navigation between questions.
- Top bar: progress pill (`4 / 10`), quit → confirm → abandon.
- App-backgrounded / refresh → resumes seamlessly via `/resume`.
- Kind badge top-left ("Specific cards" / "Global knowledge").

**`QuizResultsPage.vue`** — route `/quizzes/:id/attempts/:aid/results`
- Score ring + per-question review list (stem, your answer, correct answer, explanation, "Report" affordance).
- For specific quizzes: "Review cards" CTA jumps to flashcard editor.
- Footer: **Retake** • **Share with friends** • **Back**.
- History sparkline if ≥2 attempts.

**`QuizSharedPage.vue`** — route `/quiz-invite/:token`
- Public preview (owner, subject, kind, count).
- Expired/revoked → friendly message.
- "Take this quiz" → `/accept` → navigates to play.
- Non-subscriber recipient: counts against `quizDemoUsed` OR paywall.

### Modified pages

**`TodayPlanCard.vue`** (Spec B home card)
- Renders quiz slots as distinct rows alongside training rows.
- Tap slot:
  - `quizId` absent → call `/quizzes/generate` with `planContext` (no quota debit) → store `quizId` → navigate to play.
  - `in_progress` → resume.
  - `completed` → results.

**`SubjectDetailPage.vue` / `ChapterDetailPage.vue`**
- New "Start Quiz" row (AI-subscriber only). Locked state for non-subscribers after demo used.

**`ExamSetupPage.vue`** (Spec B)
- Intensity selector: **Light** / **Normal** / **Intense**. Default Normal.

### New components
- `MultiChoiceQuestion.vue`, `TrueFalseQuestion.vue`, `FillBlankQuestion.vue`
- `QuizKindBadge.vue` — `specific` gold, `global` purple
- `QuizResultRow.vue`, `QuizShareDialog.vue`, `QuizSlotRow.vue`

### New store

**`stores/quiz.ts`**
- State: `currentAttempt`, `currentQuestion`, `progress`, `history[]`, loading/error.
- Actions: `start`, `resume`, `answer`, `abandon`, `retake`, `generate`, `share`, `accept`.
- Never caches `correct_jsonb`.

### Copy / empty states
- No attempts: "Quiz mode tests your understanding across cards. Generate your first quiz."
- Out of quota: "You've used today's quizzes. Quota resets at midnight." + upgrade link if not subscribed.
- Zero eligible cards (global, empty subject): "Add some flashcards first."

### Navigation guards
- `/quizzes/:id/*` — auth + quiz ownership OR `source='shared_copy'` owned by caller.
- `/quiz-invite/:token` — auth required, no ownership check.

---

## 5. Control Flow

### 5.1 Generate standalone quiz
```
QuizSetupPage → POST /quizzes/generate { subjectId, kind, size, types, cardFilter }
  ├─ auth + user_has_ai_access(uid)  [402 if fail AND quizDemoUsed=true]
  ├─ aiQuotaService.Debit(user, "quiz", 1)  [atomic; 429 on cap]
  ├─ resolve pool (specific = user filter; global = AI pre-selection)
  ├─ RunStructuredGeneration(FeatureGenerateQuiz, ...)
  ├─ BEGIN TX → INSERT quizzes (source='user') + quiz_questions × N → COMMIT
  ├─ first-ever for non-subscriber? → UPDATE quizDemoUsed=true
  └─ 200 { quizId }
→ navigate /quizzes/:id/play
```

### 5.2 Play flow (linear, commit-on-answer, resumable)
```
QuizPlayPage.onMount → POST /quizzes/:id/start
  ├─ in_progress exists? return it
  ├─ else INSERT quiz_attempts (state='in_progress', total_count=N)
  └─ return { attemptId, nextQuestion, progress }

User answers → POST /attempts/:aid/answer { questionId, answer }
  ├─ load quiz_questions.correct_jsonb
  ├─ score server-side by question_type
  ├─ BEGIN TX
  │    ├─ INSERT quiz_attempt_answers (correct, user_answer_jsonb)
  │    ├─ UPDATE quiz_attempts.correct_count
  │    └─ if last:
  │         ├─ SET state='completed', completed_at, score_pct
  │         └─ if plan_id: INSERT revision_plan_progress × referenced_fc_ids
  ├─ COMMIT
  └─ 200 { correct, correctAnswer, explanation, nextQuestion|null }

Resume: GET /attempts/:aid/resume → picks up at next unanswered ordinal.
```

### 5.3 Plan-integrated quiz (lazy materialization)
```
TodayPlanCard quiz slot tap →
  POST /quizzes/generate {
    ..., planContext: { planId, planDate, slotIndex }
  }
  ├─ entitlement check unchanged
  ├─ SKIP aiQuotaService.Debit  [planQuotaCovered=true]
  ├─ generate + persist (source='plan', source_plan_id)
  ├─ UPDATE revision_plans.days_jsonb[planDate].quizSlots[i].quizId
  └─ 200 { quizId }
→ play → attempt carries plan_id + plan_date
→ completion writes revision_plan_progress → plan slot marked done
```

### 5.4 Retake (no AI call)
```
QuizResultsPage → POST /quizzes/:id/retake
  ├─ 409 if in_progress exists
  ├─ INSERT quiz_attempts (new attempt, reuses quiz_questions)
  └─ 200 { attemptId }
→ play
```

### 5.5 Share & accept (clone-on-accept)
```
Owner → POST /quizzes/:id/share { expiresAt? }
  → INSERT quiz_share_links → { token, url }

POST /quizzes/:id/send-to-friends { friendIds, shareToken }
  → INSERT quiz_sent_to_friends → emit notifications (in-app badge v1)

Recipient: GET /quizzes/shared/:token → preview (no questions)
POST /quizzes/shared/:token/accept
  ├─ reject if expired/revoked or already accepted
  ├─ entitlement check (402 + demo path)
  ├─ BEGIN TX
  │    ├─ INSERT quizzes (source='shared_copy', card_pool_jsonb frozen)
  │    ├─ INSERT quiz_questions × N (verbatim copy)
  │    └─ COMMIT
  └─ 200 { quizId }
→ play [as owned quiz]

Owner revokes: DELETE /quizzes/shares/:token → SET revoked_at
  [existing clones untouched — permanent]
```

### 5.6 Quality report (fire-and-forget)
```
QuizResultsPage per-question Report → POST /quiz-questions/:id/report { reason, note? }
→ 204. Pure telemetry; no downstream behavior in v1.
```

### 5.7 Concurrency & idempotency
- **At most one in-progress attempt per (quiz,user)**: partial UNIQUE index; retake 409s rather than overwriting.
- **Answer idempotency**: PK `(attempt_id, question_id)` — double-submit is ON CONFLICT DO NOTHING.
- **Plan slot materialization retries**: server detects existing `quizId` for `(planId, planDate, slotIndex)` and returns it.
- **Generation has no dedup key**: client disables CTA during request; user-visible cost.

### 5.8 Failure modes
- AI provider timeout → transaction aborts, quota rolled back in same TX, user sees retry.
- Malformed AI JSON → same as above; `RunStructuredGeneration` validation catches it.
- Resume on `completed` attempt → redirect to results.
- Share accept after revoke/expire → 410 Gone.
- Plan slot regen after day rollover → frontend hides stale slot from today's card; past attempts remain in history.

---

## 6. Dependencies

- **Spec A (shipped)** — `RunStructuredGeneration` pipeline, entitlement, daily quotas. Adds `quiz` counter + `quizDemoUsed` flag.
- **Spec B (approved)** — revision plan schema + progress table; this spec amends `revision_plans` (add `intensity`) and day-plan JSON (add `quizSlots`).
- **Spec C (approved)** — `user_has_ai_access(uid)` is the entitlement check; `quizDemoUsed` is the single bypass path for the one-time free demo.

---

## 7. Cross-spec constraints respected

- All AI calls route through `RunStructuredGeneration` with a new `FeatureGenerateQuiz` key. ✅
- Entitlement + quota checked inside the pipeline. ✅
- Frontend never talks to the AI provider. ✅
- Daily quota is per-feature; `quiz` joins `prompt`/`pdf`/`check`/`plan`. ✅
- Plan generation cost covers materialized plan quizzes (no double charge). ✅

---

## 8. Risks

- **Hallucination in distractors** — MCQ options that are accidentally correct. Mitigation: prompt engineering + per-question "Report" feeds `quiz_quality_reports` telemetry.
- **Fill-blank fuzzy scoring** — over-strict rejects typos; over-loose accepts wrong answers. `accepted[]` array from AI should include common variants; normalize (lower + trim + strip punctuation) before compare. If field signals a score-disputes problem, future spec can add manual override.
- **Plan intensity miscalibration** — "intense" generates more slots than user can finish; drift handling (Spec B manual rebalance) covers this.
- **Shared-copy storage growth** — every accept clones N `quiz_questions` rows. Accepted v1 cost; add cleanup policy if storage bites.
- **Quota stacking via share** — recipient plays shared quiz → no `quiz` debit (uses demo or entitlement). Intentional: share is meant as a social primitive, not a quota laundering tool. Demo flag prevents non-subscribers from replaying many shared quizzes.
- **Question length for long answers** — cards with very long answers make poor MCQ fodder. Mitigation: prompt instructs the model to truncate/summarize stems; heuristic filter in `card_pool` resolver skips cards whose answer exceeds a character budget.
- **Plan-day rollover** — user generates a plan quiz slot at 23:58 and doesn't play it; at 00:01 the day has rolled. Attempts + progress still link to the original `plan_date`. Frontend hides stale slots but results remain accessible under the quiz id.

---

## 9. Testing Strategy (high-level)

- **Unit**
  - Score computation per question type (including fuzzy fill_blank with accent/case/punctuation variants).
  - `planQuotaCovered` gate: plan context skips debit, non-plan debits.
  - Partial UNIQUE index enforces single in-progress attempt.
  - Share-copy clone isolation: modifying/revoking source does not mutate clone.
- **Integration**
  - Full generate → play → score → complete → retake loop against mocked AI.
  - Plan slot materialization: day-plan emits `quizSlots` → tap materializes → completion writes `revision_plan_progress`.
  - Share → send → recipient accept (subscriber and demo path) → independent play.
  - Abandon frees in-progress slot for immediate retake.
- **Prompt regression**
  - Golden fixtures per card shape (short, long, math, code, multilingual). Diff against stored outputs.
- **Admin / env isolation**
  - `POST /admin/reset-quiz-demo` only active when dev flag enabled; no-op in production.

---

## 10. Out-of-Scope (deferred)

- Spec E (Flashcard Duels) — competitive multiplayer over quiz questions. Spec D's share mechanism is the single-player cousin; Spec E builds the real-time layer.
- Marketplace / public quiz directory — no discovery surface in v1.
- Reshare of shared-copy quizzes — recipient cannot re-share. Keeps ownership/copy graph linear.
- AI-scored short-answer / essay — only deterministic types (MCQ, T/F, fuzzy fill_blank) in v1.
- Timed mode per question / total — surface exists in `settings_jsonb` but not wired in v1 UI.
- Difficulty gradient per quiz — `settings_jsonb.difficulty` reserved; AI prompt ignores it in v1.
- Collaborative quizzes on shared subjects — the `card_pool` snapshot on generation freezes what the quiz sees. Edits to the underlying subject after generation are not reflected.
