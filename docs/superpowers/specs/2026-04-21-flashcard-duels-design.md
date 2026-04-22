# Spec E — Flashcard Duels (Design)

**Status:** Approved — ready for implementation planning.
**Date:** 2026-04-21
**Supersedes:** [`2026-04-19-spec-e-flashcard-duels-outline.md`](2026-04-19-spec-e-flashcard-duels-outline.md)
**Scope:** Real-time 1v1 competitive quiz mode over a shared subject. 10-round head-to-head with server-authoritative scoring, WebSocket transport, reuse of Spec D `quiz_questions` as the question source. Friends + invite-token access. Challenger pays the AI quota; opponent plays free (growth hook for dragging non-subscribers into an AI experience).

Not in scope: open matchmaking / lobbies, leaderboards beyond friend head-to-head, spectator mode, tournaments, real-money stakes, async/turn-based duels, post-match chat, mobile push notifications (deferred to a later notifications spec — v1 uses in-app polling badges).

---

## 1. Architecture Overview

### High-level flow

```
┌─────────────────┐        POST /duels         ┌────────────────────┐
│ DuelSetupPage   │──────────────────────────▶│ duelService.Create │
│ pick subject,   │   { subjectId, chapterId? }│  - entitlement     │
│ invite mode     │                            │  - generate quiz   │
└─────────────────┘                            │    (Spec D, global)│
                                               │  - INSERT duels    │
                                               └──────────┬─────────┘
                                                          │
                             friends see in-app  ◀────────┤
                             ┌──────────────────┐         │
                             │ "Alice is        │         │ returns:
                             │  waiting" badge  │         │   duelId,
                             │  (polled on home)│         │   wsUrl,
                             └──────────────────┘         │   inviteToken?
                                                          │
        ┌─── Alice's client ──┐          ┌── WS ───┐      │
        │ DuelLobbyPage       │──────────│ duelHub ◀──────┘
        │ sees "Waiting…"     │ connect  │  room   │
        └─────────────────────┘          │   per   │
                                         │ duelId  │
        ┌─── Bob's client ────┐          │         │
        │ DuelInvitePage      │──────────│         │
        │ (token or friend    │ connect  │         │
        │  badge tap)         │          │         │
        └─────────────────────┘          └────┬────┘
                                              │ broadcasts:
                                              │  opponent_joined, countdown,
                                              │  question, round_result,
                                              │  duel_result, opponent_disconnected,
                                              │  opponent_reconnected, opponent_forfeit
                                              ▼
                          DuelPlayPage ←→ DuelResultsPage
```

### The round loop (server-authoritative)

```
Server broadcasts QUESTION N (stem, options, timer 20s|10s)
                       ↓
   Both players see it simultaneously (server-stamped roundStartedAt)
                       ↓
   Player A taps option ──▶ WS { answer } ──▶ server scores server-side
                       ↓
   Per-round queue: first correct answer = round winner (awarded_point=TRUE).
   Subsequent correct answers recorded (correct=TRUE, awarded_point=FALSE).
                       ↓
   On both-answered OR timer expiry → broadcast ROUND_RESULT
     { correctAnswer, explanation, roundWinnerId, score, ownAnswerCorrect }
                       ↓
   3s pause → QUESTION N+1 (or duel_result after round 10)
```

### Backend module map
- `internal/duelHub/` — WebSocket hub. Per-duel room structs with connection pair, state machine, broadcast channel. Stateless between restarts (rehydrates from DB).
- `api/service/duelService.go` — create, join, run rounds, score, finalize, reconnect, forfeit, record stats. Owns the state machine.
- `api/handler/duelHandler.go` — HTTP endpoints + WS upgrade endpoint.
- `api/service/quizService.go` — **reused verbatim**. Duel creation calls `quizService.Generate(kind='global', size=10, types=['multi_choice','true_false'], source='duel')`.
- `api/service/userReportsService.go` — new thin service for `user_reports` rows.

### Frontend module map
- `stores/duel.ts` — Pinia store wrapping WS + duel state.
- `pages/DuelSetupPage.vue`, `DuelLobbyPage.vue`, `DuelPlayPage.vue`, `DuelResultsPage.vue`, `DuelInvitePage.vue`.
- `components/duel/DuelScorecard.vue`, `DuelWaitingBadge.vue`, `DuelRoundResult.vue`, `DuelResultRow.vue`, `DuelReportDialog.vue`, `DuelInviteLinkDialog.vue`.

### Hard boundaries
- **All AI generation** goes through `RunStructuredGeneration` with `FeatureGenerateQuiz`. No new feature key. Duel quizzes set `quizzes.source='duel'` and constrain types to MCQ + T/F.
- **Only `duelService`** writes `duels`, `duel_round_questions`, `duel_round_answers`, `duel_invite_tokens`. Quiz tables remain owned by `quizService`.
- **Scoring is server-side only.** Client never sees `correct_jsonb`. Round-winner computation happens in `duelService.handleAnswer()`; broadcast emits `{correct, roundWinnerId}` after round resolves.
- **Quota charged on challenger only.** Opponent plays without entitlement check — growth hook so paying users can pull non-subscribers into a full AI experience.
- **Quiz snapshots are frozen at duel creation.** `duels.quiz_id ON DELETE RESTRICT`; `quiz_questions` survive the duel permanently.
- **WebSocket hub is stateless between restarts.** Duel state lives in Postgres. On boot, hub rehydrates any duel with state in ('countdown','active','paused') and restarts pause timers where applicable.

---

## 2. Data Model

### New tables

**`duels`** — one row per duel match.
```sql
CREATE TABLE duels (
    id BIGSERIAL PRIMARY KEY,
    challenger_id BIGINT NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    opponent_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    quiz_id BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE RESTRICT,
    subject_id BIGINT REFERENCES subjects(id) ON DELETE SET NULL,
    challenger_username_snapshot TEXT NOT NULL,
    opponent_username_snapshot TEXT,
    subject_name_snapshot TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'waiting','countdown','active','paused',
        'completed','expired','forfeited')),
    current_round INT NOT NULL DEFAULT 0,
    score_challenger INT NOT NULL DEFAULT 0,
    score_opponent INT NOT NULL DEFAULT 0,
    winner_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    is_draw BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    paused_at TIMESTAMPTZ,
    paused_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX ON duels (challenger_id, created_at DESC);
CREATE INDEX ON duels (opponent_id, created_at DESC);
CREATE INDEX ON duels (state) WHERE state IN ('waiting','countdown','active','paused');
CREATE UNIQUE INDEX ON duels (challenger_id) WHERE state='waiting';
```

**`duel_invite_tokens`** — out-of-app share links.
```sql
CREATE TABLE duel_invite_tokens (
    token TEXT PRIMARY KEY,
    duel_id BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX ON duel_invite_tokens (duel_id);
```

**`duel_round_questions`** — frozen round-ordering snapshot.
```sql
CREATE TABLE duel_round_questions (
    duel_id BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_number INT NOT NULL CHECK (round_number BETWEEN 1 AND 10),
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE RESTRICT,
    round_started_at TIMESTAMPTZ,
    round_ended_at TIMESTAMPTZ,
    timer_seconds INT NOT NULL,
    PRIMARY KEY (duel_id, round_number)
);
```

**`duel_round_answers`** — per-round, per-player answer log.
```sql
CREATE TABLE duel_round_answers (
    duel_id BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_number INT NOT NULL CHECK (round_number BETWEEN 1 AND 10),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE RESTRICT,
    user_answer_jsonb JSONB NOT NULL,
    correct BOOLEAN NOT NULL,
    answered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    server_receipt_ts TIMESTAMPTZ NOT NULL,
    awarded_point BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (duel_id, round_number, user_id)
);
CREATE INDEX ON duel_round_answers (question_id);
```

**`duel_user_stats`** — denormalized per-user aggregate.
```sql
CREATE TABLE duel_user_stats (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    wins INT NOT NULL DEFAULT 0,
    losses INT NOT NULL DEFAULT 0,
    draws INT NOT NULL DEFAULT 0,
    forfeits_received INT NOT NULL DEFAULT 0,
    forfeits_caused INT NOT NULL DEFAULT 0,
    best_perfect_score BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**`duel_head_to_head`** — per-pair stats.
```sql
CREATE TABLE duel_head_to_head (
    user_a_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wins_a INT NOT NULL DEFAULT 0,
    wins_b INT NOT NULL DEFAULT 0,
    draws INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_a_id, user_b_id),
    CHECK (user_a_id < user_b_id)
);
```

**`user_reports`** — generic abuse reporting.
```sql
CREATE TABLE user_reports (
    id BIGSERIAL PRIMARY KEY,
    reporter_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reported_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    context_type TEXT NOT NULL CHECK (context_type IN ('duel','friend_request','other')),
    context_id BIGINT,
    reason TEXT NOT NULL CHECK (reason IN (
        'harassment','offensive_username','spam','cheating','other')),
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON user_reports (reported_user_id);
CREATE INDEX ON user_reports (reporter_id);
```

Question reports reuse Spec D's `quiz_quality_reports` — no new table.

### Amendments to existing tables

```sql
ALTER TABLE quizzes DROP CONSTRAINT quizzes_source_check;
ALTER TABLE quizzes ADD CONSTRAINT quizzes_source_check
    CHECK (source IN ('user','plan','shared_copy','duel'));
```

### Achievement catalogue additions (server-side)
- `first_duel` — "First duel played"
- `first_duel_win` — "First duel won"
- `duel_streak_3` / `duel_streak_10` — "Win N duels in a row"
- `duels_won_10` / `duels_won_50` — "Total duels won"
- `perfect_duel` — "Win a duel 10-0"
- `friend_defeater_5` — "Beat 5 different friends at least once"
- `rematch_king` — "Win a rematch after losing the original"

Category `'duels'` added to the existing `mastery | streak | volume | exploration` enum.

### Key invariants
- **At most one `waiting` duel per challenger.** Partial UNIQUE index (above).
- **Round answer is write-once.** PK `(duel_id, round_number, user_id)` — double-submit is ON CONFLICT DO NOTHING.
- **`awarded_point` is computed at round resolution.** TRUE on the earlier-receipted correct answer; never re-assigned.
- **Subject/username snapshots frozen at duel creation.** Historical integrity over freshness.
- **Stats tables are derived.** Rebuildable from `duels`-completed rows. Trigger-driven on INSERT/UPDATE of `duels` → state='completed' | 'forfeited'.
- **Quiz row persists as long as the duel does.** `duels.quiz_id ON DELETE RESTRICT`.

---

## 3. Backend Endpoints

### Duel lifecycle
- **`POST /duels`** — body `{ subjectId, chapterId?, inviteMode: 'friends'|'token'|'both' }`
  - Auth + entitlement on challenger (or `quizDemoUsed` path)
  - Enforces one-waiting-room rule; 409 if violated
  - `quizService.Generate(kind='global', size=10, types=['multi_choice','true_false'], source='duel')` → debits `aiQuota.quiz`
  - INSERT `duels` + `duel_round_questions` × 10 + `duel_invite_tokens` (if token mode)
  - Returns `{ duelId, wsUrl, inviteToken?, inviteUrl? }`

- **`POST /duels/:id/join`** — body `{ token? }`
  - Auth + validation (state='waiting', caller ≠ challenger, token valid if provided)
  - Atomic `UPDATE ... WHERE state='waiting'` transitions state to 'countdown'
  - Hub broadcasts `opponent_joined`
  - Returns `{ duelId, wsUrl }`

- **`GET /duels/:id`** — metadata fetch. Participant ACL. Questions returned without `correct_jsonb`.

- **`POST /duels/:id/forfeit`** — explicit quit. State → 'forfeited', other participant wins.

- **`POST /duels/:id/rematch`** — challenger only. Creates new duel with opponent_id pre-populated. Fresh quiz generation → debits quota again.

### WebSocket
- **`GET /ws/duels/:id`** — WS upgrade. Auth via JWT; validates participant; rejects others.
  - Client messages: `{type:'ready'}`, `{type:'answer', round, questionId, answer}`, `{type:'ping'}`
  - Server messages: `opponent_joined`, `countdown`, `question`, `round_result` (with personalized `ownAnswerCorrect`), `duel_result` (with `newlyUnlockedAchievements`), `opponent_disconnected`, `opponent_reconnected`, `opponent_forfeit`, `error`

### Results & history
- **`GET /duels/:id/results`** — full per-round review for completed/forfeited duels.
- **`GET /duels/history?opponentId=&limit=&cursor=`** — paginated own duel history.
- **`GET /duels/stats/me`** — overall + per-friend head-to-head.
- **`GET /duels/stats/:userId`** — friend's stats (friends-only ACL; 403 for non-friends). Returns head-to-head vs caller only; never reveals stats against third parties.

### Discovery
- **`GET /duels/waiting-from-friends`** — lightweight poll (15s) for home badge. Returns friends currently in `waiting` state.

### Invite tokens
- **`GET /duel-invite/:token`** — preview `{ duelId, challengerUsername, subjectName, expired, alreadyFilled }`. Auth required.
- **`DELETE /duels/invites/:token`** — revoke (challenger only).

### Reports
- **`POST /duels/:id/report-user`** — body `{ reportedUserId, reason, note? }` → INSERT `user_reports` (context='duel'). Telemetry only.
- **`POST /quiz-questions/:id/report`** — reused from Spec D.

### Background jobs
- **`duel-expirer`** (cron 60s) — waiting rooms >10min → state='expired'; revokes associated tokens. Quota NOT refunded.
- **`duel-timeout`** (hub internal) — paused-duel 60s elapsed → forfeit disconnected player.
- **`duel-stats-rebuilder`** (`POST /admin/rebuild-duel-stats`, dev-gated) — rebuilds denormalized stats tables from `duels`.

### Admin / dev
- **`POST /admin/force-expire-duel/:id`** (dev-gated) — QA unstick.

All non-admin endpoints require verified auth. Entitlement enforced on challenger only (creation + rematch). Opponent is never gated by subscription — only by "is a participant in this duel".

---

## 4. Frontend UX

### New pages

**`DuelSetupPage.vue`** — `/duel/new`
- Subject picker (owned + collaborated + subscribed).
- Optional chapter narrower.
- Invite-mode toggle: **Friend only** (picker) / **Invite link** / **Both** (default).
- Friend picker: reuses existing friend-list component; single-select.
- Quota badge + "Start duel room" CTA.
- Non-subscriber first-visit: demo banner; after demo used → paywall.

**`DuelLobbyPage.vue`** — `/duel/:id/lobby`
- Challenger view: "Waiting for opponent…" + spinner, invite-link copy affordance, Cancel CTA.
- Opponent view: "Ready to duel!" with both avatars face-to-face, auto-transitions on `countdown`.
- 10-min countdown visible to challenger.
- Both players tap "Ready" → server starts 3-2-1 countdown → auto-navigates to play.

**`DuelPlayPage.vue`** — `/duel/:id/play`
- Top bar: challenger avatar + live score + opponent avatar + round counter `3/10`.
- Question area: stem markdown + options as tap cards (MCQ) or two big buttons (T/F).
- Per-round timer ring: 20s (MCQ) or 10s (T/F).
- After own answer: options freeze, "Waiting for opponent…" state.
- On `round_result`: reveals correct answer + explanation + round-winner banner; 3s pause.
- Quit button top-left → confirm → `/forfeit`.
- Network-drop overlay: "Reconnecting…" (own) or "Bob is reconnecting… 47s" (opponent).
- Router guard blocks nav-away unless forfeit confirmed.

**`DuelResultsPage.vue`** — `/duel/:id/results`
- Hero: winner avatar + final-score display.
- Freshly-unlocked achievements as toasts on mount.
- Per-round review list.
- Report CTAs (user + per-question).
- CTAs: **Rematch** (challenger only), **Suggest rematch** (opponent — signals challenger), **Share result** (text card copyable), **Back**.
- History sparkline: vs-this-opponent W-L-D trail for last 10 matches.

**`DuelInvitePage.vue`** — `/duel-invite/:token`
- Preview card via `GET /duel-invite/:token`.
- Expired/revoked/filled → friendly message + home.
- Accept CTA → `/duels/:id/join` → lobby.

### Modified pages / components

**`HomePage.vue`**
- `DuelWaitingBadge` strip when friends are in waiting state. Visible in both AI and Reactive modes (duels are social, not gamification).
- Polls `/duels/waiting-from-friends` every 15s.

**`ProfilePage.vue`**
- "Duels" section for friends-only visibility: overall W-L-D + duel achievements.
- Mutual friend profiles: head-to-head record + "Challenge to duel" CTA.

**`SubjectDetailPage.vue` / `ChapterDetailPage.vue`**
- "Challenge a friend" action row (AI-subscriber gated; locked state for non-subscribers post-demo).

### New components
- `DuelScorecard.vue` — compact live score.
- `DuelWaitingBadge.vue` — home-screen discovery card.
- `DuelRoundResult.vue` — post-round reveal panel.
- `DuelResultRow.vue` — per-round results-review row.
- `DuelReportDialog.vue` — report user + question combined modal.
- `DuelInviteLinkDialog.vue` — lobby link + copy + friend-picker fallback.

### New store

**`stores/duel.ts`**
- State: `currentDuel`, `wsStatus`, `currentRound`, `currentQuestion`, `timerRemaining`, `scores`, `ownAnswer`, `roundResult`, `opponentConnected`, `opponentReconnectCountdown`, `finalResult`, `waitingFriends`, `history`, `stats`.
- Actions: `create`, `join`, `connect`, `disconnect`, `markReady`, `submitAnswer`, `forfeit`, `requestRematch`, `suggestRematch`, `reportUser`, `reportQuestion`, `fetchWaitingFriends`, `fetchHistory`, `fetchStats`.
- WS handler: single dispatch switch.

### Navigation guards
- `/duel/:id/*` — auth + participant ACL; non-participants → 403.
- `/duel/:id/play` — requires state in ('countdown','active','paused'); completed → redirect to results; expired → home.
- `/duel-invite/:token` — auth required (no ownership check).
- `beforeEach` for `/duel/:id/play` blocks nav-away unless explicit forfeit.

### Copy / empty states
- No friends: friend-mode disabled with tooltip; invite-link mode remains available.
- Duel expired: "Nobody joined in time. Try again?" → setup with config pre-filled.
- Opponent forfeit mid-play: "Bob left. You win by forfeit." → results.
- Question reported: "Thanks — we'll review it." toast; duel flow unchanged.
- Empty history: "No duels yet. Challenge a friend from their profile."

### Design system alignment
- `DuelScorecard` uses widget style (#18181A bg, 12px radius, 20px padding).
- Score numbers: Main-text weight at Title scale (32px) for drama.
- Winner banner: Succeed green (you won) / Danger red (opponent won) / Text white (draw).
- Timer ring: Primary blue >50%, Warning orange <50%, Danger red <20%.

---

## 5. Control Flow

### 5.1 Create duel (challenger)
```
DuelSetupPage → POST /duels { subjectId, chapterId?, inviteMode }
  ├─ auth + entitlement (challenger only; demo path allowed once)
  ├─ 409 if challenger has an existing waiting room (partial UNIQUE)
  ├─ quizService.Generate(kind='global', size=10, types=['mcq','tf'], source='duel')
  │    ├─ aiQuotaService.Debit(challenger, "quiz", 1)
  │    └─ RunStructuredGeneration(...) → INSERT quizzes + quiz_questions × 10
  ├─ BEGIN TX
  │    ├─ INSERT duels (state='waiting', snapshots populated)
  │    ├─ INSERT duel_round_questions × 10 (timer derived from question_type)
  │    └─ if inviteMode in ('token','both'): INSERT duel_invite_tokens
  ├─ COMMIT
  └─ 200 { duelId, wsUrl, inviteToken?, inviteUrl? }
→ navigate /duel/:id/lobby → open WS
```

### 5.2 Opponent joins
```
Friend: HomePage polls /duels/waiting-from-friends → sees Alice → tap Join
Token:  Opens /duel-invite/:token → preview → Accept

POST /duels/:id/join { token? }
  ├─ auth + validation (state='waiting', caller ≠ challenger, token valid)
  ├─ UPDATE duels SET opponent_id, snapshot, state='countdown' WHERE state='waiting'
  │    [atomic race guard — second joiner gets 0 rows → 409]
  ├─ hub broadcasts 'opponent_joined' to challenger
  └─ 200 { duelId, wsUrl }
→ navigate /duel/:id/lobby → open WS → receive state snapshot
```

### 5.3 Countdown & first question
```
Both send {type:'ready'} → duelHub tracks ready-set
When both ready:
  ├─ broadcast {type:'countdown', seconds: 3→2→1}
  ├─ UPDATE duels SET state='active', current_round=1, started_at=NOW()
  ├─ UPDATE duel_round_questions SET round_started_at=NOW() WHERE round=1
  └─ broadcast {type:'question', round:1, question{id,type,stem,options}, timerSeconds, roundStartedAt}
```

### 5.4 Round play (server-authoritative)
```
Player answers → WS {type:'answer', round, questionId, answer}

duelHub.handleAnswer:
  ├─ reject double-tap (answers_received[userId])
  ├─ server_receipt_ts = NOW()
  ├─ correct = score(question, answer)
  ├─ awarded_point = correct AND (first_correct_receipt == null)
  ├─ INSERT duel_round_answers (..., correct, server_receipt_ts, awarded_point)
  ├─ update first_correct_receipt if applicable
  └─ check resolution (both answered OR timer expired)

Round resolution:
  ├─ BEGIN TX
  │    ├─ compute round_winner_id from awarded_point=TRUE row (NULL if nobody correct)
  │    ├─ UPDATE duels SET score_challenger/opponent += 1 if applicable
  │    └─ UPDATE duel_round_questions SET round_ended_at=NOW()
  ├─ COMMIT
  └─ broadcast {type:'round_result', round, correctAnswer, explanation,
                 roundWinnerId, scoreChallenger, scoreOpponent, ownAnswerCorrect}
  ├─ 3s pause
  └─ if round<10: advance; else: finalize (§5.5)
```

### 5.5 Duel resolution
```
Round 10 resolved:
  ├─ BEGIN TX
  │    ├─ winner_id = higher score; tie → is_draw=TRUE
  │    ├─ UPDATE duels SET state='completed', winner_id, is_draw, completed_at=NOW()
  │    ├─ stats trigger: update duel_user_stats, duel_head_to_head
  │    └─ evaluate duel achievements → INSERT unlocked rows; collect for broadcast
  ├─ COMMIT
  └─ broadcast {type:'duel_result', winnerId, isDraw, finalScore, newlyUnlocked}
→ clients navigate /duel/:id/results
```

### 5.6 Disconnect / reconnect
```
WS closes → duelHub.onDisconnect:
  ├─ if state in ('countdown','active'):
  │    ├─ UPDATE duels SET state='paused', paused_at=NOW(), paused_by_user_id
  │    ├─ freeze in-round timer
  │    └─ broadcast 'opponent_disconnected' to remaining
  └─ schedule duel-timeout (60s)

Reconnect:
  ├─ auth + user matches paused_by_user_id
  ├─ UPDATE duels SET state='active', paused_at=NULL, paused_by_user_id=NULL
  ├─ resume timer from frozen value
  └─ broadcast 'opponent_reconnected' + send state snapshot to reconnecter

Timeout (60s elapsed):
  ├─ UPDATE duels SET state='forfeited', winner_id=present_player_id, completed_at=NOW()
  ├─ stats trigger (forfeits_received +1, forfeits_caused +1)
  └─ broadcast 'opponent_forfeit' → clients → /duel/:id/results
```

### 5.7 Explicit forfeit
```
POST /duels/:id/forfeit (from Quit button)
  ├─ auth + participant + state in ('countdown','active','paused')
  ├─ UPDATE duels SET state='forfeited', winner_id=other, completed_at=NOW()
  ├─ stats trigger
  ├─ broadcast 'opponent_forfeit' to the other player
  └─ 200
```

### 5.8 Rematch
```
Challenger on DuelResultsPage → Rematch → POST /duels/:id/rematch
  ├─ auth + was-challenger + original state in ('completed','forfeited')
  ├─ quizService.Generate(...) → fresh 10 questions, debits quota
  ├─ INSERT duels (state='waiting', opponent_id pre-populated to prior opponent)
  ├─ INSERT duel_round_questions × 10
  └─ 200 { duelId, wsUrl }

Broadcast 'rematch_proposed' on previous-duel WS (still open on results page):
  → opponent sees Accept/Decline prompt → Accept = POST /duels/:newId/join
```

### 5.9 Waiting-room expiration
```
cron duel-expirer (60s):
  UPDATE duels SET state='expired'
    WHERE state='waiting' AND created_at < NOW() - INTERVAL '10 min';
  UPDATE duel_invite_tokens SET revoked_at=NOW()
    WHERE duel_id IN (above);
[aiQuota NOT refunded — quiz was generated and sits in DB.]
```

### 5.10 Reporting (fire-and-forget)
```
DuelResultsPage → DuelReportDialog:
  POST /duels/:id/report-user → 204 (Spec E)
  POST /quiz-questions/:id/report → 204 (Spec D)
```

### 5.11 Concurrency & idempotency
- **At most one waiting duel per challenger**: partial UNIQUE index; 409.
- **Join race safety**: `UPDATE ... WHERE state='waiting'` — second joiner gets 0 rows → 409.
- **Answer idempotency per round**: PK `(duel_id, round, user_id)` + in-memory guard.
- **First-correct point is singular**: `awarded_point=TRUE` on at most one row per round.
- **Pause/resume guarded**: `WHERE state=X` in UPDATEs blocks double-transition.
- **WS reconnect safety**: hub keyed on `(duel_id, user_id)`; new connection replaces old.

### 5.12 Failure modes
- **Quiz generation fails**: transaction aborts, quota refunded in-TX, no duel row.
- **Mid-countdown disconnect**: state→paused before first question; 60s timer; forfeit on timeout.
- **Both players disconnect**: paused → if both reconnect, resume; if neither, duel-timeout expires (winner=NULL, state='expired'; no forfeit-stat update to either side).
- **Server restart mid-duel**: hub rehydrates `duels` in non-terminal states; old paused_at respected if <60s, else starts fresh timer.
- **Challenger deletes subject mid-duel**: ON DELETE SET NULL; snapshot frozen; duel continues.
- **Question reported for bad content**: telemetry only; duel continues normally.

---

## 6. Dependencies

- **Spec A (shipped)** — `RunStructuredGeneration`, entitlement, `aiQuota.quiz` counter, `quizDemoUsed` flag.
- **Spec C (approved)** — `user_has_ai_access(uid)` SQL function for challenger gating.
- **Spec D (approved)** — `quiz_questions` table as the question source; `quizzes.source='duel'` added; `quiz_quality_reports` reused for bad-question flagging.

---

## 7. Cross-spec constraints respected

- All AI generation routes through `RunStructuredGeneration` with `FeatureGenerateQuiz`. ✅
- Entitlement checked inside the pipeline at `quizService.Generate`. ✅
- Frontend never talks to the AI provider. ✅
- Quiz questions are immutable post-generation (Spec D invariant) — duels freeze the round order via `duel_round_questions` snapshot table. ✅
- Quiz share is the non-competitive share primitive (Spec D) — duels handle competitive multiplayer, with no leaderboards added to quiz sharing. ✅

---

## 8. New cross-spec constraints (for future specs)

- **Head-to-head scoring is server-authoritative**. Any future competitive features must rely on server receipt-time, not client-reported timestamps.
- **WebSocket hub is stateless**. Duel state in Postgres is the source of truth; hub instances are replaceable.
- **Snapshot critical social context on creation**. Usernames + subject names frozen at duel time to survive source deletions. Future multiplayer features should do the same.
- **Challenger-pays is the default cost model for 1v1 competitive features**. The inviter bears the AI cost; the invitee plays for free. This doubles as a growth primitive.

---

## 9. Risks

- **WebSocket reliability on Capacitor WebView**: mobile WS through Capacitor is non-trivial. Need to validate the plugin (`@capacitor-community/websockets` or native) handles background/foreground transitions, iOS backgrounding (5s kill), and Android doze mode. Worst case: longer reconnect windows on known-mobile clients.
- **Stats drift**: trigger-driven denormalized stats can drift if triggers fail silently. `POST /admin/rebuild-duel-stats` is the escape hatch; add a daily consistency check in v1.1 if drift becomes real.
- **Question quality in live play**: a bad AI-generated question during a duel is worse than during a solo quiz (the opponent sees it too). `quiz_quality_reports` + duel-context review should prioritize duel-reported questions for prompt tuning.
- **Race on opponent join under invite-token sharing**: Alice posts the token to a group chat; Bob and Carol both tap. Atomic `UPDATE WHERE state='waiting'` handles this — whoever commits first wins, second gets 409. UX copy on the loser's side: "Someone joined first."
- **Spam duel-creation**: waiting-room lifetime is 10 min; at one duel per 10 min per challenger, ~144 duels/day theoretical max. Each costs one `quiz` debit, so quota already gates it. No additional rate-limiting needed in v1.
- **Achievement evaluation cost**: on duel completion we evaluate ~9 achievements per participant. Trivial cost. No concern.
- **Subject-rename staleness**: snapshot is frozen intentionally; if user complains "but I renamed it!", that's a feature per Q14, not a bug.

---

## 10. Testing Strategy (high-level)

- **Unit**
  - `duelService.handleAnswer` first-correct-wins-round logic (including tied receipts — deterministic tiebreak by user_id).
  - Pause/resume state machine (valid transitions only).
  - Timer computation from question_type.
  - Stats trigger correctness under completed / forfeited / expired terminal states.

- **Integration**
  - Full lifecycle: create → join → countdown → 10 rounds → results → stats updated → achievements unlocked.
  - Disconnect-at-round-3 → reconnect-within-60s → continue.
  - Disconnect-at-round-3 → timeout → forfeit → stats recorded correctly.
  - Challenger forfeits mid-round → opponent gets win.
  - Duel-expirer job: 11-min-old waiting room → expired, token revoked, quota not refunded.
  - Two opponents race to join → one wins, the other gets 409.
  - Rematch flow: new quiz generated, opponent pre-populated, history links resolved.

- **WebSocket**
  - Reconnect after brief drop preserves state.
  - Server restart mid-duel rehydrates correctly.
  - Non-participant WS connect rejected.
  - Message ordering: question → answer → round_result → next question.

- **Admin / env isolation**
  - `POST /admin/rebuild-duel-stats` only active under dev flag.

---

## 11. Out-of-Scope (deferred)

- **Open matchmaking / lobby** — stranger-contact vectors, harassment infra, underage compliance. v2 minimum.
- **Leaderboards** — any beyond friend head-to-head. Public rankings need a discovery and privacy story.
- **Tournaments / brackets** — more than 2 players at once. Different subsystem.
- **Spectators** — watching a friend's live duel. Interesting but not worth WS fan-out complexity in v1.
- **Post-match chat** — harassment vector; minimal value on top of existing friendship features.
- **Async / turn-based duels** — contradicts the sync-mode architectural choice; different state machine.
- **Push notifications** — Capacitor push infra belongs to a later spec. v1 uses in-app polling (`duels/waiting-from-friends`) as the discovery surface.
- **Mobile IAP for non-subscribers to play duels** — Spec C.1 will eventually cover this.
- **Achievement showcase / profile cosmetics** — reactive-mode users don't see achievements in UI; no pressure to visualize them beyond the existing grid.
- **Duel history beyond last 10 matches against a specific friend** — hard-capped in v1 UI; full history accessible via `GET /duels/history` but no paginated UI.
- **Block list** — Spec E reports but doesn't block. If invite-token abuse surfaces in telemetry, block-list infra is a small follow-up.
