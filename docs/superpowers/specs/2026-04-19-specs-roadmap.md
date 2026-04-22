# StudBud Specs Roadmap

**Updated:** 2026-04-19
**Purpose:** Single index of every major design spec for StudBud — what's shipped, what's designed, what's next, and how they depend on each other. Each row points to the authoritative design doc (or outline, for not-yet-brainstormed specs).

---

## Status Legend

- **Approved** — Full design doc written, brainstormed, approved. Ready for (or in) implementation.
- **Outline** — Scope captured, dependencies known, open questions listed. Needs a brainstorming pass before becoming a full spec.
- **Idea** — Mentioned as future work but no scope capture yet.

## Specs

| # | Spec | Status | Doc | Depends on |
|---|------|--------|-----|------------|
| A | AI Flashcard Generation & AI Check | Approved (in impl) | [`2026-04-18-ai-flashcard-generation-design.md`](2026-04-18-ai-flashcard-generation-design.md) | — |
| B.0 | Flashcard Keyword Index | Approved | [`2026-04-19-flashcard-keyword-index-design.md`](2026-04-19-flashcard-keyword-index-design.md) | A (AI pipeline primitive) |
| B | AI Revision Plan | Approved | [`2026-04-19-ai-revision-plan-design.md`](2026-04-19-ai-revision-plan-design.md) | A, B.0 |
| C | Subscription Billing (web / Stripe) | Approved | [`2026-04-21-subscription-billing-design.md`](2026-04-21-subscription-billing-design.md) | A (entitlement stub it replaces) |
| C.1 | Subscription Billing (mobile IAP) | Idea | — | C |
| D | AI Quiz Generation | Approved | [`2026-04-21-ai-quiz-design.md`](2026-04-21-ai-quiz-design.md) | A (pipeline), B (plan integration), C (entitlement) |
| E | Flashcard Duels | Approved | [`2026-04-21-flashcard-duels-design.md`](2026-04-21-flashcard-duels-design.md) | A (pipeline), C (entitlement), D (quiz questions) |
| F | Gamification Server-Sync | Tracking note | See §"Follow-ups" below | — |

## Suggested Order

```
Spec A ──► Spec B.0 ──► Spec B
  │
  └──► Spec C (unlocks real paying users)
  │
  └──► Spec D (AI quiz — reuses Spec A pipeline)
         │
         └──► Spec E (duels — reuses Spec D quiz questions + WS infra)
```

**Rationale:**
- A → B.0 → B is the AI-value chain. Every AI feature after A gets cheaper because B.0 fans keywords out to everything downstream.
- C is the commercial gate. It replaces Spec A's `ai_subscription_active` admin-flip with real billing. Should ship before public launch.
- D and E are both post-launch features. E now depends on D — duels reuse `quiz_questions` rather than inventing a parallel question model. Shipping D first means E's implementation is net-new WebSocket infra only, not a new AI primitive.
- F is a standalone wiring task — the backend already exposes the endpoints; the Pinia stores just need to switch from in-memory to server-persisted. Not a full spec, captured below for tracking.

## Cross-Spec Constraints (Locked)

These decisions are authoritative across specs. Don't re-litigate in later brainstorming without revisiting earlier designs.

| Constraint | Source | What it means for downstream specs |
|------------|--------|------------------------------------|
| All AI calls route through `RunStructuredGeneration` | Spec A §3.2 | Specs D, any future AI feature must register a `FeatureKey` and reuse the pipeline. No new provider glue. |
| Entitlement + quota checked inside the pipeline | Spec A §3.2 | Can't bypass by adding handlers. Spec D's quiz, Spec B's plan, Spec B.0's extractor all go through it. |
| Frontend never talks to the AI provider | Spec A §3.2 | Mobile client (Capacitor) must not hold API keys. Applies forever. |
| `ai_subscription_active` is a stub (admin-flipped) | Spec A §2 | Replaced by Spec C. Until then, any entitlement-gated feature uses the stub flag. |
| Keyword index is system-side cost, no user quota | Spec B.0 §2 | Downstream readers of `flashcard_keywords` don't need to charge users for keyword access. |
| B.0 has no HTTP endpoints | Spec B.0 §2 | Downstream features access keywords only via SQL from backend services. |
| One exam = one subject | Spec B §2 | Multi-subject exams deferred; don't assume `exam → [subject]` anywhere. |
| Plan quota separate from prompt/pdf/check | Spec B §2 | Spec C must surface `plan` in the billing tier definition. |
| Annales debit Spec A's `pdf.pagesUsed` | Spec B §2 | Single shared PDF counter. Spec D shouldn't invent a parallel one. |
| Quiz kinds are `specific` or `global`, never hybrid | Spec D §1 | UI and data model enforce two distinct kinds; future quiz-like features must not mix card-pool and free-form question modes in one row. |
| Plan-materialized quizzes skip the `quiz` quota debit | Spec D §1 | Plan generation pre-pays via `planQuotaCovered`. Future plan extensions that spawn AI work must follow the same pre-pay pattern (no double-charging). |
| Quiz questions are immutable post-generation | Spec D §2 | Fixing bad questions = regenerate quiz. Downstream features (duels, analytics) can snapshot `quiz_questions` rows as stable references. |
| Shared-copy quizzes are independent of the source | Spec D §5.5 | Revoking a share link does not mutate clones. Spec E (duels) inherits this — if duels reuse quiz questions, decide whether a duel snapshots or links. |
| Quiz share is the non-competitive share primitive | Spec D §10 | Spec E handles competitive multiplayer. Don't add scoring/leaderboards to quiz sharing. |
| Head-to-head scoring is server-authoritative | Spec E §8 | Future competitive features must rely on server receipt-time, not client-reported timestamps. |
| WebSocket hubs are stateless | Spec E §8 | Real-time state lives in Postgres. Hub instances are replaceable; rehydrate from DB on boot. |
| Snapshot critical social context on creation | Spec E §8 | Usernames + subject names frozen at row creation to survive source deletions. Any future multi-party feature should do the same. |
| Challenger-pays is the default for 1v1 competitive features | Spec E §8 | Inviter bears AI cost; invitee plays free. Doubles as growth primitive (free users can taste AI via invites). |

## Follow-ups (Non-Spec, But Tracked)

### F — Gamification Server-Sync

**What:** The backend already exposes `/get-gamification-state`, `/get-achievements`, `/record-training-session`, `/get-preferences`, `/update-preferences`, `/update-daily-goal`, `/get-user-stats` (per CLAUDE.md). The Pinia stores `gamification` and `appMode` still read/write in-memory. Swap them over.

**Why not a full spec:** No new product decisions. No new endpoints. No architectural choices. It's wiring: replace in-memory mutations with API calls, handle loading/error states, invalidate on login/logout.

**Trigger for doing it:** Before Spec B ships. The revision plan's daily-goal interaction (see Spec B §6.3) assumes `doneToday` is server-authoritative. If we ship B on top of an in-memory store, a user who closes and reopens the app loses their plan progress.

**Scope when picked up:**
- Replace `stores/gamification.ts` in-memory fields with API-backed reactive refs.
- Wire `/record-training-session` at end of training (currently training just updates cards, doesn't close the session).
- On login: fetch state + preferences + achievements.
- Cache-invalidate on `/update-preferences`, `/update-daily-goal`.
- Show newly-unlocked achievements from `recordTrainingSession` response as toasts.
- Handle offline gracefully (queue session record, retry on reconnect — or defer to Capacitor's network plugin).

**Estimated size:** 1–2 days of focused work; no design round needed.

### Other ideas (not yet scoped)

- **Offline mode** — full offline support with conflict resolution. Non-trivial. Capacitor-specific. No design yet.
- **Push notifications** — daily study reminders, friend requests, duel invites. Requires Capacitor push plugin + backend notification service. No design yet.
- **Analytics / insights** — "You studied X cards this week, most time on Y subject." Depends on server-side event log. No design yet.
- **Image search / OCR** — take a photo of a notebook page, extract text, create flashcards. AI-heavy, expensive. Probably post-launch v2.
- **Spaced repetition engine** — replace `dueHeuristic` with a real SRS algorithm (SM-2 or FSRS). Significant scope. No design yet.
- **Export / import** — Anki deck export, CSV import, etc. No design yet.

These are intentionally vague — noted so they don't get lost, but not demanding design effort until product direction is clearer.
