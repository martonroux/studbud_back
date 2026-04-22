# Spec E — Flashcard Duels (Outline) — SUPERSEDED

**Status:** Superseded by full spec [`2026-04-21-flashcard-duels-design.md`](2026-04-21-flashcard-duels-design.md).
**Date:** 2026-04-19
**Scope:** A social/competitive mode where two users face off on a shared set of flashcards (a subject, a chapter, or a chosen pool). Both answer the same cards; the app scores speed + accuracy and declares a winner. Friend-challenge primary; open matchmaking maybe.

Not in scope: tournaments with more than 2 players, real-money stakes, global leaderboards (maybe v2), spectator/streaming, asynchronous tournament brackets.

---

## Purpose

Turn solo studying into a social ritual. Students revise with friends informally all the time ("quiz me"); duels make that a first-class app feature. Two benefits:
1. **Motivation loop** — friends keep each other accountable, creating retention beyond streaks and daily goals.
2. **Engagement surface** — duel invites double as "open the app" triggers, valuable for retention metrics.

Duels run over flashcards (not AI-generated quiz questions — that's Spec D). Each card is presented to both players, both tap a result (correct / partial / incorrect) on the flipped card, or — optionally — type/pick an answer if Spec D's quiz questions are integrated.

## Dependencies

- **None required.** Can be built standalone.
- **Friends system** — already exists (friend requests, friendships table). Duels use it for challenge-by-username.
- **Optional: Spec D** — if we want duels to use AI-generated multi-choice questions for objective scoring instead of self-graded flips. Major product decision, below.

## Known Decisions (carved out from prior specs)

- None — this is the first mention of duels in any spec.
- Implicitly inherits StudBud's access model (Spec A + core): only subjects the user has viewer-or-higher access to can be dueled over.

## Key Open Questions (resolve during brainstorming)

### Product
1. **Sync vs async** — real-time (both players online, card by card together) or async (player A completes their run, player B has 24h to match)? Async is vastly simpler and more forgiving of life.
2. **Challenge source** — friends-only, or open matchmaking from a lobby? (If matchmaking, privacy and cheating become live concerns.)
3. **Subject choice** — challenger picks subject, or both pick and app intersects? What if opponent has no access to the subject? (Copy-subject flow kicks in? Duel blocked?)
4. **Card pool** — random N from subject, all cards, last-week's cards, bad/OK cards only? Configurable per duel or fixed?
5. **Duel length** — fixed (10 cards), time-boxed (60s), or "first to X wins"?
6. **Scoring model** — speed + accuracy combined? Pure accuracy? What about self-graded (honor system) vs objective (Spec D quiz questions)?
7. **Self-grading honor system** — in sync mode, can the opponent see whether you graded yourself honestly? Does it even matter for casual play?
8. **Rewards** — duel wins give XP? Achievements? Visible record in profile ("12 duels won")?
9. **Rematch flow** — one-tap rematch with same config?
10. **Blocking / ignoring** — can user decline duel invites? Can they block a friend from sending duels? (Friend removal already exists — maybe sufficient.)
11. **Notifications** — push notif on duel invite? In-app badge? Email?
12. **Tie-breaking** — cleanly possible, or show "draw"?
13. **Ghosting handling** — challenger waits 3 days for opponent in async mode → duel auto-expires. Challenger gets refunded… what, exactly? Nothing?

### Technical
14. **Real-time transport** — if sync mode, WebSockets or Server-Sent Events + long-poll? Capacitor support for WS? Offline drops = ?
15. **Duel state persistence** — long-lived server-side state (turns, deadlines, scores) vs ephemeral (just "completed" rows)?
16. **Cheat resistance** — trivial to cheat in self-graded mode. Acceptable? Or do we force Spec D objective questions for competitive duels?
17. **Scale** — how many concurrent duels do we expect? Is a goroutine-per-duel room acceptable or do we need a dedicated matchmaking service?
18. **Mobile app lifecycle** — user backgrounds the app mid-duel; does the duel pause, continue with timeout, or auto-concede?
19. **Shared card IDs** — if a duel references FCs in the challenger's subject, but the opponent only has viewer access through a subscription, what if the challenger later deletes a card? Snapshot FCs into the duel, or resolve live?

### Regulatory / safety
20. **Underage users** — if we allow open matchmaking (not friends-only), we inherit stranger-contact issues. Friends-only avoids this entirely.
21. **Reporting / abuse** — need a report flow if strangers play together.

## Architectural Sketch (non-binding)

### Async-duel model (simpler, recommended as v1)

```
┌─────────────┐     POST /duels/challenge      ┌──────────────────┐
│ Challenger  │───────────────────────────────▶│  duel row CREATED │
└─────────────┘  { opponentUsername, config }   │  state=pending    │
                                                └──────────────────┘
                                                          │
Challenger runs card pool offline-like ────────┐          │
POST /duels/:id/submit-run { scores[] }        │          ▼
                                               │    state=awaiting_opponent
                                               │          │
Push notif to opponent ────────────────────────┼──────────┘
                                               │
Opponent opens, plays same cards ──────────────┤
POST /duels/:id/submit-run { scores[] }        │
                                               ▼
                                        state=completed
                                        scoring computed
                                        both users see result screen
```

### Real-time duel (sync mode, v2 if we want it)

WebSocket-backed "room" abstraction:
```
[challenger] ─┐
              ├──▶ wsHub ◀── [opponent]
[cards emitted one at a time, both reply, move on]
```

Requires significant ops work: reconnection, spectator race conditions, clock sync.

### New modules (tentative)
- `api/service/duelService.go` — challenge creation, run submission, scoring.
- `api/handler/duelHandler.go` — endpoints.
- Schema: `duels(id, challenger_id, opponent_id, subject_id, config_jsonb, state, winner_id, created_at, completed_at)` + `duel_cards(duel_id, fc_id, order)` + `duel_runs(duel_id, user_id, scores_jsonb, submitted_at)`.
- If sync: `internal/duelHub/` WebSocket manager.

### Frontend
- `pages/DuelInvitePage.vue` — pick friend, subject, config.
- `pages/DuelPlayPage.vue` — runs the card set like training, reports results on submit.
- `pages/DuelResultsPage.vue` — winner, breakdown, rematch CTA.
- `components/duel/DuelInviteCard.vue` — shown in Home / Notifications when someone challenges you.
- New notification type for duel invite / duel completed.

## Risks

- **Scope creep** — sync mode + matchmaking + leaderboards + rewards is multiple specs of work. Start async + friend-only.
- **Sparse network** — if users don't have friends on the app yet (early stage), the feature looks empty. Consider offering "duel with the bot" as a placeholder.
- **Abuse** — if matchmaking opens, harassment vectors appear (profile names, post-match chat). Friend-only defers this indefinitely.
- **Self-grading honor** — if duels matter for pride/rankings, dishonest self-grading erodes trust fast. Forces us toward Spec D integration for competitive play.
- **Notification fatigue** — duel invites easily become spam. Per-user invite rate limits and a mute-from-this-user option will be needed.

## Testing Strategy (high-level)

- Unit: scoring formula with ties, timeouts, partial runs.
- Integration: full async duel lifecycle (challenge → both runs → complete → winner written).
- Friends-only enforcement: user tries to duel a stranger → 403.
- Subject access: user tries to duel on a subject the opponent can't see → 400 with helpful error.

## Next Step Before Full Spec

Two big product forks that change everything:
1. **Async-first or sync-first?** Decides whether we need real-time infra.
2. **Self-graded or AI-scored?** Ties Spec E to Spec D if we go AI-scored.

These are the first questions for brainstorming. Most other questions fall out once these are pinned.
