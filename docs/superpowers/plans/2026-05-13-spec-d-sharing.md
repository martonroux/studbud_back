# Spec D Part 3 — Sharing, Quality, Demo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Spec D's social + telemetry surface: shareable quiz links, send-to-friends (with clone-on-accept), per-question quality reports, and the one-shot free demo for non-subscribers. Excludes the quiz core (Plan D1) and revision-plan integration (Plan D2).

**Architecture:** Share tokens live in `quiz_share_links` (already schema-reconciled in Plan D1). Accept clones the source quiz + questions verbatim into a new `quizzes` row with `source='shared_copy'`; revoking the original share token does not affect existing clones (per spec §1 invariant). The "free demo" path bypasses the entitlement check exactly once per user via the existing `ai_quota_daily.quiz_demo_used` flag. Quality reports are pure telemetry — they insert into `quiz_quality_reports` and have no downstream side-effects in v1. Notifications are deliberately minimal: `quiz_sent_to_friends` rows are the only persisted side-effect; surfacing them in a UI badge is deferred to a separate notifications plan.

**Tech Stack:** Same as Plans D1/D2 — Go 1.25 + pgx + stdlib `net/http`; `pkg/quiz` for domain logic; Postgres for state.

**Spec reference:** `docs/superpowers/specs/2026-04-21-ai-quiz-design.md` §3 (sharing endpoints), §3 (quality), §5.5 (share flow), §5.6 (quality), §3 (admin/dev), §2 invariants (clone independence + demo).

**Hard dependency:** Plan D1 merged. Plan D2 is **not** a prerequisite (this plan can ship independently of D2, since neither path mutates plan rows).

**Prerequisite reading for the implementer:**
- Spec D design doc, §3 (sharing/admin), §5.5-5.6
- `pkg/quiz/service.go`, `pkg/quiz/persist.go`, `pkg/quiz/attempt.go` (Plan D1)
- `api/handler/quiz_stub.go` (Plan D1 — handler skeleton we extend)
- `pkg/access/service.go` (`HasAIAccess`) — current entitlement check
- `db_sql/setup_ai.go:36-37` (`quiz_demo_used` already in `ai_quota_daily`)
- `db_sql/setup_core.go:123-131` (`friendships` table — needed for send-to-friends validation)

---

## Phase 1 — One-shot free demo

Before sharing/sending, ship the demo gate: it changes the shape of the entitlement check used by `POST /quizzes/generate` and `POST /quizzes/shared/:token/accept`.

### Task 1: Service-level demo gate

`pkg/quiz` already enforces AI access in the handler via `access.HasAIAccess`. Wrap that with a new method on the quiz service that:
1. Returns `nil` if the user has full AI access.
2. Returns `nil` if the user has `quiz_demo_used = false` (and atomically flips it to `true`).
3. Otherwise returns `myErrors.ErrNoAIAccess`.

The flip must happen at the same time as the check to avoid a "double demo" race.

**Files:**
- Create: `pkg/quiz/demo.go`
- Test: `pkg/quiz/demo_test.go`

- [ ] **Step 1: Write failing tests**

```go
package quiz_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestEntitleQuiz_SubscriberAlwaysAllowed(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	svc := quiz.NewService(pool, nil)
	if err := svc.EntitleQuiz(context.Background(), u.ID); err != nil {
		t.Fatalf("subscriber rejected: %v", err)
	}
	// Subscriber path must NOT flip the demo flag.
	if testutil.QuizDemoUsed(t, pool, u.ID) {
		t.Fatalf("demo flag flipped for subscriber")
	}
}

func TestEntitleQuiz_NonSubscriber_FirstCallAllowed_FlipsFlag(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	// no GiveAIAccess

	svc := quiz.NewService(pool, nil)
	if err := svc.EntitleQuiz(context.Background(), u.ID); err != nil {
		t.Fatalf("first demo rejected: %v", err)
	}
	if !testutil.QuizDemoUsed(t, pool, u.ID) {
		t.Fatalf("demo flag should be true after first call")
	}
}

func TestEntitleQuiz_NonSubscriber_SecondCallRejected(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	svc := quiz.NewService(pool, nil)

	if err := svc.EntitleQuiz(context.Background(), u.ID); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := svc.EntitleQuiz(context.Background(), u.ID)
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("second call should be 402, got %v", err)
	}
}
```

`testutil.QuizDemoUsed` is a one-line helper to add to `testutil/seed.go`:

```go
func QuizDemoUsed(t *testing.T, pool *pgxpool.Pool, uid int64) bool {
	t.Helper()
	var used bool
	_ = pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT quiz_demo_used FROM ai_quota_daily WHERE user_id=$1 AND day=CURRENT_DATE), false)`, uid,
	).Scan(&used)
	return used
}
```

- [ ] **Step 2: Run to verify failure**

```
go test ./pkg/quiz/ -run 'TestEntitleQuiz' -v
```

Expected: BUILD FAIL — `EntitleQuiz` undefined.

- [ ] **Step 3: Implement `pkg/quiz/demo.go`**

```go
package quiz

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// AccessChecker is the slice of pkg/access this package needs.
type AccessChecker interface {
	HasAIAccess(ctx context.Context, uid int64) (bool, error)
}

// SetAccess wires the entitlement dependency. Called from cmd/app/deps.go.
func (s *Service) SetAccess(a AccessChecker) {
	s.access = a
}

// EntitleQuiz returns nil iff the caller may run a quiz operation that
// consumes the AI surface (generate or accept-shared). Order:
//   1. If user has AI access -> allow without touching the demo flag.
//   2. Otherwise atomically flip quiz_demo_used from false to true; allow.
//   3. Demo already used -> ErrNoAIAccess.
func (s *Service) EntitleQuiz(ctx context.Context, uid int64) error {
	ok, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	flipped, err := flipDemoFlag(ctx, s.db, uid)
	if err != nil {
		return err
	}
	if !flipped {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// flipDemoFlag returns true iff the caller successfully transitioned
// quiz_demo_used from false to true today.
func flipDemoFlag(ctx context.Context, pool *pgxpool.Pool, uid int64) (bool, error) {
	tag, err := pool.Exec(ctx, `
		INSERT INTO ai_quota_daily (user_id, day, quiz_demo_used)
		VALUES ($1, CURRENT_DATE, true)
		ON CONFLICT (user_id, day) DO UPDATE
		   SET quiz_demo_used = true
		 WHERE ai_quota_daily.quiz_demo_used = false`, uid)
	if err != nil {
		return false, fmt.Errorf("flip demo flag:\n%w", err)
	}
	return tag.RowsAffected() > 0, nil
}
```

In `pkg/quiz/service.go` add the `access` field:

```go
type Service struct {
	db     *pgxpool.Pool
	ai     AIDriver
	access AccessChecker
}
```

(Constructor signature stays the same; wiring is done via `SetAccess` to avoid breaking D1 callers. If preferred, add `access AccessChecker` as the third constructor argument and update all call sites in one go.)

In `cmd/app/deps.go`, after constructing `d.quiz`, call `d.quiz.SetAccess(d.access)`.

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestEntitleQuiz' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/demo.go pkg/quiz/service.go pkg/quiz/demo_test.go cmd/app/deps.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: one-shot free demo via quiz_demo_used

[+] Service.EntitleQuiz: subscriber -> allow; first non-sub -> flip flag + allow; second -> 402
[+] flipDemoFlag uses UPDATE … WHERE quiz_demo_used = false for atomic transition
[+] cmd/app/deps.go wires access into pkg/quiz.Service
EOF
)"
```

### Task 2: Swap the handler's entitlement check to `EntitleQuiz`

`api/handler/quiz_stub.go.requireAIAccess` currently calls `access.HasAIAccess` directly. Route generate + accept-share through `EntitleQuiz` instead.

**Files:**
- Modify: `api/handler/quiz_stub.go` (replace `requireAIAccess` body)
- Test: `api/handler/quiz_demo_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestPostQuizzesGenerate_DemoPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool) // NO AI access
	sid := testutil.NewSubject(t, pool, u.ID, "Bio")

	// First call: demo allowed.
	body, _ := json.Marshal(map[string]any{
		"subjectId": sid, "kind": "global", "size": 5,
		"types": []string{"multi_choice"},
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = testutil.WithAuthedUser(req, u.ID)
	w := httptest.NewRecorder()
	h := testutil.NewQuizHandlerForTest(t, pool, withFakeAI())
	h.Generate(w, req)
	if w.Code != 200 {
		t.Fatalf("first call status %d; body=%s", w.Code, w.Body.String())
	}

	// Second call: demo exhausted -> 402.
	req2 := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req2 = testutil.WithAuthedUser(req2, u.ID)
	w2 := httptest.NewRecorder()
	h.Generate(w2, req2)
	if w2.Code != http.StatusPaymentRequired {
		t.Fatalf("second call status %d, want 402", w2.Code)
	}
}
```

(`testutil.NewQuizHandlerForTest` is a small helper that constructs the handler with a fake AI client. Add it once and reuse across D1/D2/D3 tests.)

- [ ] **Step 2: Run to verify failure**

Expected: FAIL — current handler uses `HasAIAccess` which rejects the first call too.

- [ ] **Step 3: Update the handler**

In `api/handler/quiz_stub.go`, replace `requireAIAccess`:

```go
// requireAIAccess routes through Service.EntitleQuiz so non-subscribers get
// exactly one free demo per Spec D.
func (h *QuizHandler) requireAIAccess(ctx context.Context, uid int64) error {
	return h.svc.EntitleQuiz(ctx, uid)
}
```

(The constructor no longer needs `*access.Service` — the access dep lives inside `*quiz.Service`. Remove the field + parameter, update `cmd/app/routes.go`.)

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestPostQuizzesGenerate_DemoPath|TestPostQuizzesGenerate_HappyPath|TestPostQuizzesGenerate_NoAIAccess' -v
```

Expected: PASS — including the D1 `NoAIAccess` test, which still passes because `EntitleQuiz` returns `ErrNoAIAccess` after the demo is spent.

- [ ] **Step 5: Commit**

```bash
git add api/handler/quiz_stub.go cmd/app/routes.go api/handler/quiz_demo_test.go
git commit -m "$(cat <<'EOF'
Spec D: handler entitlement -> EntitleQuiz (subscriber OR one-shot demo)

[&] requireAIAccess delegates to Service.EntitleQuiz
[&] QuizHandler no longer needs *access.Service (dep moved into quiz.Service)
[+] handler test: first-call demo allowed; second-call 402
EOF
)"
```

### Task 3: `POST /admin/reset-quiz-demo`

Dev-only endpoint to clear `quiz_demo_used` for the calling test/QA tier. Per spec §3 it is only active when the dev flag is enabled; in production it is a no-op (404 / 403).

**Files:**
- Modify: `api/handler/admin_ai.go` (or `admin_quiz.go` — see Step 1)
- Modify: `cmd/app/routes.go` (`registerAdminRoutes`)
- Test: `api/handler/admin_quiz_test.go`

- [ ] **Step 1: Decide handler location**

```
ls api/handler/admin_*.go
```

Existing admin handlers: `admin_ai.go`, `admin_billing.go`. Add a new file `admin_quiz.go` to keep concerns separated. The pattern is: a handler struct, a constructor, one method per route. Mirror `admin_billing.go`'s shape.

- [ ] **Step 2: Write failing test**

`api/handler/admin_quiz_test.go`:

```go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/testutil"
)

func TestPostAdminResetQuizDemo_ClearsFlag(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	admin := testutil.NewAdminUser(t, pool)
	target := testutil.NewVerifiedUser(t, pool)
	testutil.MarkQuizDemoUsed(t, pool, target.ID) // helper to set the flag manually

	h := handler.NewAdminQuizHandler(pool, /* devMode = */ true)
	req := httptest.NewRequest("POST", "/admin/reset-quiz-demo?userId="+itoa(target.ID), nil)
	req = testutil.WithAuthedUser(req, admin.ID)
	w := httptest.NewRecorder()
	h.ResetDemo(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
	if testutil.QuizDemoUsed(t, pool, target.ID) {
		t.Fatalf("flag still true")
	}
}

func TestPostAdminResetQuizDemo_DisabledInProd(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	h := handler.NewAdminQuizHandler(pool, false) // dev mode off

	req := httptest.NewRequest("POST", "/admin/reset-quiz-demo?userId=1", nil)
	w := httptest.NewRecorder()
	h.ResetDemo(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 in prod mode, got %d", w.Code)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 4: Implement `api/handler/admin_quiz.go`**

```go
package handler

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
)

// AdminQuizHandler hosts dev/admin-only quiz operations.
type AdminQuizHandler struct {
	db      *pgxpool.Pool // db is the shared pool
	devMode bool          // devMode gates the endpoint in production
}

// NewAdminQuizHandler constructs an AdminQuizHandler.
func NewAdminQuizHandler(db *pgxpool.Pool, devMode bool) *AdminQuizHandler {
	return &AdminQuizHandler{db: db, devMode: devMode}
}

// ResetDemo handles POST /admin/reset-quiz-demo?userId=N. Only active when devMode is true.
func (h *AdminQuizHandler) ResetDemo(w http.ResponseWriter, r *http.Request) {
	if !h.devMode {
		httpx.WriteError(w, myErrors.ErrNotFound)
		return
	}
	raw := r.URL.Query().Get("userId")
	uid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE ai_quota_daily SET quiz_demo_used = false
		   WHERE user_id = $1 AND day = CURRENT_DATE`, uid,
	); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

In `cmd/app/routes.go.registerAdminRoutes` add:

```go
adminQuizH := handler.NewAdminQuizHandler(d.db, d.cfg.DevMode)
mux.Handle("POST /admin/reset-quiz-demo", adm(adminQuizH.ResetDemo))
```

If `d.cfg.DevMode` doesn't exist yet, plumb a `DevMode bool` field through `deps` / `cfg`, defaulting to `false`. Document the env var (`STUDBUD_DEV_MODE`) in `.env.example` if your config-loader reads env vars.

- [ ] **Step 5: Run tests to verify they pass**

```
go test ./api/handler/ -run 'TestPostAdminResetQuizDemo' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add api/handler/admin_quiz.go api/handler/admin_quiz_test.go cmd/app/routes.go cmd/app/deps.go .env.example
git commit -m "$(cat <<'EOF'
Spec D: POST /admin/reset-quiz-demo (dev-only)

[+] AdminQuizHandler.ResetDemo: clears today's quiz_demo_used for ?userId=N
[+] 404 in prod mode (gated by deps.cfg.DevMode)
[+] .env.example documents STUDBUD_DEV_MODE
EOF
)"
```

---

## Phase 2 — Share token lifecycle

### Task 4: `POST /quizzes/:id/share`

**Files:**
- Create: `pkg/quiz/share.go`
- Test: `pkg/quiz/share_test.go`
- Modify: `api/handler/quiz_stub.go` (add `CreateShare`)

- [ ] **Step 1: Write failing service test**

```go
package quiz_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestCreateShare_ReturnsToken(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	svc := quiz.NewService(pool, nil)
	share, err := svc.CreateShare(context.Background(), u.ID, qid, nil)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if len(share.Token) < 32 {
		t.Fatalf("token too short: %q", share.Token)
	}

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_share_links WHERE quiz_id=$1`, qid).Scan(&n)
	if n != 1 {
		t.Fatalf("share row count = %d, want 1", n)
	}
}

func TestCreateShare_NonOwner_403(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	stranger := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, owner.ID, 1)

	svc := quiz.NewService(pool, nil)
	_, err := svc.CreateShare(context.Background(), stranger.ID, qid, nil)
	if !errors.Is(err, myErrors.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestCreateShare_WithExpiry_PersistsExpiresAt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	exp := time.Now().Add(72 * time.Hour).UTC()
	svc := quiz.NewService(pool, nil)
	share, err := svc.CreateShare(context.Background(), u.ID, qid, &exp)
	if err != nil {
		t.Fatalf("share: %v", err)
	}

	var stored time.Time
	_ = pool.QueryRow(context.Background(),
		`SELECT expires_at FROM quiz_share_links WHERE token=$1`, share.Token,
	).Scan(&stored)
	if stored.Truncate(time.Second) != exp.Truncate(time.Second) {
		t.Fatalf("stored %v want %v", stored, exp)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement `pkg/quiz/share.go`**

```go
package quiz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"studbud/backend/internal/myErrors"
)

// Share is the projection returned to clients of CreateShare.
type Share struct {
	Token     string     `json:"token"`
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// CreateShare mints a 64-char hex token for the quiz. expiresAt is optional
// (nil = never expires). Caller must own the quiz.
func (s *Service) CreateShare(ctx context.Context, uid, quizID int64, expiresAt *time.Time) (Share, error) {
	if err := s.requireQuizOwner(ctx, uid, quizID); err != nil {
		return Share{}, err
	}
	token, err := newShareToken()
	if err != nil {
		return Share{}, err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO quiz_share_links (token, quiz_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4)`,
		token, quizID, uid, expiresAt,
	); err != nil {
		return Share{}, fmt.Errorf("insert share:\n%w", err)
	}
	return Share{
		Token:     token,
		URL:       fmt.Sprintf("/quiz-invite/%s", token),
		ExpiresAt: expiresAt,
	}, nil
}

// newShareToken returns a 64-char (32-byte) hex string drawn from crypto/rand.
func newShareToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random:\n%w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// requireShareOwner returns ErrForbidden if token wasn't created by uid.
func (s *Service) requireShareOwner(ctx context.Context, uid int64, token string) error {
	var owner int64
	err := s.db.QueryRow(ctx,
		`SELECT created_by FROM quiz_share_links WHERE token=$1`, token,
	).Scan(&owner)
	if err != nil {
		return myErrors.ErrNotFound
	}
	if owner != uid {
		return myErrors.ErrForbidden
	}
	return nil
}
```

- [ ] **Step 4: Run service tests**

```
go test ./pkg/quiz/ -run 'TestCreateShare' -v
```

Expected: PASS.

- [ ] **Step 5: Add the handler**

In `api/handler/quiz_stub.go`:

```go
type createShareRequest struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// CreateShare handles POST /quizzes/{id}/share.
func (h *QuizHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	var body createShareRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)); return
		}
	}
	share, err := h.svc.CreateShare(r.Context(), uid, qid, body.ExpiresAt)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, share)
}
```

Add the route in `cmd/app/routes.go` next to the other quiz routes:

```go
mux.Handle("POST /quizzes/{id}/share", av(quizH.CreateShare))
```

- [ ] **Step 6: Run handler test (smoke)**

```
go test ./api/handler/ -run 'TestPostQuizzesShare' -v
```

Expected: PASS (if you wrote a corresponding handler test; if not, the smoke check `go build ./... && go test ./...` is sufficient for this task).

- [ ] **Step 7: Commit**

```bash
git add pkg/quiz/share.go pkg/quiz/share_test.go api/handler/quiz_stub.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec D: POST /quizzes/{id}/share

[+] CreateShare mints 32-byte hex token, optional expiry
[+] requireShareOwner helper for revoke/send-to-friends
[+] handler POST /quizzes/{id}/share
EOF
)"
```

### Task 5: `DELETE /quizzes/shares/:token`

Revoke a share. Per Spec D §1 invariant: revoke does **not** affect clones already taken — only sets `revoked_at` on the link.

**Files:**
- Modify: `pkg/quiz/share.go`
- Modify: `api/handler/quiz_stub.go`
- Test: `pkg/quiz/share_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestRevokeShare_SetsRevokedAt(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	svc := quiz.NewService(pool, nil)
	share, _ := svc.CreateShare(context.Background(), u.ID, qid, nil)
	if err := svc.RevokeShare(context.Background(), u.ID, share.Token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var rev *time.Time
	_ = pool.QueryRow(context.Background(),
		`SELECT revoked_at FROM quiz_share_links WHERE token=$1`, share.Token,
	).Scan(&rev)
	if rev == nil {
		t.Fatalf("revoked_at still null")
	}
}

func TestRevokeShare_NonOwner_403(t *testing.T) {
	// ... seed owner + stranger ...
	err := svc.RevokeShare(ctx, stranger.ID, share.Token)
	if !errors.Is(err, myErrors.ErrForbidden) { t.Fatalf("want forbidden") }
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement**

Append to `pkg/quiz/share.go`:

```go
// RevokeShare flips revoked_at. Does not delete the row — existing accepted
// clones remain independent rows owned by the recipient.
func (s *Service) RevokeShare(ctx context.Context, uid int64, token string) error {
	if err := s.requireShareOwner(ctx, uid, token); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx,
		`UPDATE quiz_share_links SET revoked_at = now()
		   WHERE token=$1 AND revoked_at IS NULL`, token)
	if err != nil {
		return fmt.Errorf("revoke share:\n%w", err)
	}
	return nil
}
```

Handler in `api/handler/quiz_stub.go`:

```go
// RevokeShare handles DELETE /quizzes/shares/{token}.
func (h *QuizHandler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	token := r.PathValue("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput); return
	}
	if err := h.svc.RevokeShare(r.Context(), uid, token); err != nil {
		httpx.WriteError(w, err); return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Route:

```go
mux.Handle("DELETE /quizzes/shares/{token}", av(quizH.RevokeShare))
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestRevokeShare' -v
go test ./api/handler/ -run 'TestDeleteQuizShare' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/share.go pkg/quiz/share_test.go api/handler/quiz_stub.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec D: DELETE /quizzes/shares/{token}

[+] RevokeShare flips revoked_at; clones unaffected (spec invariant)
[+] handler + route
EOF
)"
```

### Task 6: `GET /quizzes/shared/:token`

Public preview (auth still required — Spec D §3 says "auth required, no ownership check"). Returns `{ owner, subjectTitle, kind, questionCount, expired }` with **no questions**.

**Files:**
- Modify: `pkg/quiz/share.go` (`SharePreview`)
- Modify: `api/handler/quiz_stub.go` (`PreviewShare`)
- Test: `pkg/quiz/share_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestSharePreview_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	sid := testutil.NewSubject(t, pool, owner.ID, "Bio")
	qid := testutil.NewQuizWithSubject(t, pool, owner.ID, sid, 5)
	svc := quiz.NewService(pool, nil)
	share, _ := svc.CreateShare(context.Background(), owner.ID, qid, nil)

	prev, err := svc.SharePreview(context.Background(), share.Token)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Owner != owner.Username {
		t.Fatalf("owner = %q", prev.Owner)
	}
	if prev.SubjectTitle != "Bio" {
		t.Fatalf("subjectTitle = %q", prev.SubjectTitle)
	}
	if prev.QuestionCount != 5 {
		t.Fatalf("questionCount = %d", prev.QuestionCount)
	}
	if prev.Expired {
		t.Fatalf("not yet expired")
	}
}

func TestSharePreview_Revoked_Returns410(t *testing.T) {
	// ... mint share, revoke, then preview ...
	_, err := svc.SharePreview(ctx, share.Token)
	if !errors.Is(err, myErrors.ErrGone) {
		t.Fatalf("want ErrGone, got %v", err)
	}
}

func TestSharePreview_Expired_Returns410(t *testing.T) {
	// ... mint share with expiresAt in the past ...
	_, err := svc.SharePreview(ctx, share.Token)
	if !errors.Is(err, myErrors.ErrGone) {
		t.Fatalf("want ErrGone, got %v", err)
	}
}
```

`myErrors.ErrGone` may not yet exist; if so, add to `internal/myErrors/errors.go`:

```go
// ErrGone indicates a share link that has been revoked or expired.
var ErrGone = errors.New("gone")
```

And register the status mapping (search `internal/httpx/errors.go` for `mapStatus` and add a case returning `http.StatusGone` / 410).

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement**

Append to `pkg/quiz/share.go`:

```go
// SharePreviewResult is the public projection (no questions).
type SharePreviewResult struct {
	Owner         string `json:"owner"`
	SubjectTitle  string `json:"subjectTitle"`
	Kind          Kind   `json:"kind"`
	QuestionCount int    `json:"questionCount"`
	Expired       bool   `json:"expired"`
}

// SharePreview returns metadata about the shared quiz. Returns ErrGone when
// the link is revoked or expired.
func (s *Service) SharePreview(ctx context.Context, token string) (SharePreviewResult, error) {
	var (
		out       SharePreviewResult
		expiresAt *time.Time
		revokedAt *time.Time
	)
	err := s.db.QueryRow(ctx, `
		SELECT u.username,
		       sub.name,
		       q.kind,
		       q.question_count,
		       sl.expires_at,
		       sl.revoked_at
		  FROM quiz_share_links sl
		  JOIN quizzes  q   ON q.id = sl.quiz_id
		  JOIN subjects sub ON sub.id = q.subject_id
		  JOIN users    u   ON u.id = q.user_id
		 WHERE sl.token = $1`, token,
	).Scan(&out.Owner, &out.SubjectTitle, &out.Kind, &out.QuestionCount, &expiresAt, &revokedAt)
	if err != nil {
		return out, myErrors.ErrNotFound
	}
	if revokedAt != nil {
		return out, myErrors.ErrGone
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		out.Expired = true
		return out, myErrors.ErrGone
	}
	return out, nil
}
```

Handler:

```go
// PreviewShare handles GET /quizzes/shared/{token}.
func (h *QuizHandler) PreviewShare(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput); return
	}
	prev, err := h.svc.SharePreview(r.Context(), token)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, prev)
}
```

Route (auth-required but no ownership; use `av`):

```go
mux.Handle("GET /quizzes/shared/{token}", av(quizH.PreviewShare))
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestSharePreview' -v
go test ./internal/httpx/ -v   # ensure ErrGone -> 410 mapping
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/share.go pkg/quiz/share_test.go api/handler/quiz_stub.go cmd/app/routes.go internal/myErrors/errors.go internal/httpx/errors.go
git commit -m "$(cat <<'EOF'
Spec D: GET /quizzes/shared/{token}

[+] SharePreview returns {owner, subjectTitle, kind, questionCount, expired}
[+] ErrGone (410) for revoked or expired links
[+] handler + route
EOF
)"
```

---

## Phase 3 — Accept + send-to-friends

### Task 7: `POST /quizzes/shared/:token/accept` — clone-on-accept

The cornerstone of Spec D's social model. On accept:
1. Validate token (not revoked, not expired).
2. Entitlement gate (subscriber OR demo).
3. Reject if caller has already accepted this token (look up `quizzes` with `source='shared_copy'`, `source_share_token=token`, `user_id=caller`).
4. Clone the source quiz row + every `quiz_questions` row verbatim into a new owner-scoped quiz with `source='shared_copy'`.

**Files:**
- Modify: `pkg/quiz/share.go`
- Modify: `api/handler/quiz_stub.go`
- Test: `pkg/quiz/share_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestAcceptShare_ClonesQuizAndQuestions(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, owner.ID)
	recipient := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, recipient.ID)
	sid := testutil.NewSubject(t, pool, owner.ID, "Bio")
	srcQID := testutil.NewQuizWithSubject(t, pool, owner.ID, sid, 3)

	svc := quiz.NewService(pool, nil)
	share, _ := svc.CreateShare(context.Background(), owner.ID, srcQID, nil)

	cloneID, err := svc.AcceptShare(context.Background(), recipient.ID, share.Token)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if cloneID == srcQID {
		t.Fatalf("clone returned source id")
	}

	// Clone is owned by recipient with source='shared_copy' + source_share_token=share.Token.
	var owner2 int64
	var src, tok string
	_ = pool.QueryRow(context.Background(),
		`SELECT user_id, source, source_share_token FROM quizzes WHERE id=$1`, cloneID,
	).Scan(&owner2, &src, &tok)
	if owner2 != recipient.ID || src != "shared_copy" || tok != share.Token {
		t.Fatalf("clone meta wrong: owner=%d src=%q tok=%q", owner2, src, tok)
	}

	// Question count identical.
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_questions WHERE quiz_id=$1`, cloneID).Scan(&n)
	if n != 3 {
		t.Fatalf("cloned questions = %d, want 3", n)
	}
}

func TestAcceptShare_AlreadyAccepted_ReturnsExistingClone(t *testing.T) {
	// ... call AcceptShare twice; assert same id ...
}

func TestAcceptShare_NoAIAccess_OneShotDemo(t *testing.T) {
	// ... recipient has no AI access; first accept allowed (demo);
	// second accept (different token from a different share) rejected ...
}

func TestAcceptShare_Revoked_410(t *testing.T) {
	// ... revoke before accept ...
}

func TestAcceptShare_RevokedAfter_AlreadyTakenStillWorks(t *testing.T) {
	// Per spec §1: revoke does NOT touch existing clones. Already-cloned recipients
	// can keep playing.
	// ... recipient accepts, owner revokes, recipient plays clone -> success ...
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement `AcceptShare`**

Append to `pkg/quiz/share.go`:

```go
// AcceptShare clones the shared quiz into the recipient's account.
// Idempotent: returns the existing clone id if the user has already accepted this token.
func (s *Service) AcceptShare(ctx context.Context, uid int64, token string) (int64, error) {
	// 1. Idempotency: existing clone?
	var existing int64
	err := s.db.QueryRow(ctx, `
		SELECT id FROM quizzes
		 WHERE user_id=$1 AND source='shared_copy' AND source_share_token=$2`,
		uid, token,
	).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	// 2. Token validity.
	if _, err := s.SharePreview(ctx, token); err != nil {
		return 0, err
	}
	// 3. Entitlement (subscriber OR demo).
	if err := s.EntitleQuiz(ctx, uid); err != nil {
		return 0, err
	}
	// 4. Clone in one tx.
	return s.cloneSharedQuiz(ctx, uid, token)
}

// cloneSharedQuiz performs the BEGIN -> INSERT quizzes -> INSERT quiz_questions
// (verbatim copy) -> COMMIT. The source quiz id is resolved via the share link.
func (s *Service) cloneSharedQuiz(ctx context.Context, uid int64, token string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin clone tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var srcQuizID int64
	if err := tx.QueryRow(ctx,
		`SELECT quiz_id FROM quiz_share_links WHERE token=$1`, token,
	).Scan(&srcQuizID); err != nil {
		return 0, fmt.Errorf("resolve source quiz:\n%w", err)
	}

	// Clone the quizzes row.
	var cloneID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO quizzes (
			user_id, subject_id, chapter_id, kind, source, source_share_token,
			card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash
		)
		SELECT $1, q.subject_id, q.chapter_id, q.kind, 'shared_copy', $2,
		       q.card_pool_jsonb, q.settings_jsonb, q.question_count, q.model, q.prompt_hash
		  FROM quizzes q WHERE q.id = $3
		RETURNING id`, uid, token, srcQuizID,
	).Scan(&cloneID); err != nil {
		return 0, fmt.Errorf("insert clone quiz:\n%w", err)
	}

	// Clone every question verbatim.
	if _, err := tx.Exec(ctx, `
		INSERT INTO quiz_questions (
			quiz_id, ordinal, question_type, stem,
			options_jsonb, correct_jsonb, explanation, referenced_fc_ids_jsonb
		)
		SELECT $1, ordinal, question_type, stem,
		       options_jsonb, correct_jsonb, explanation, referenced_fc_ids_jsonb
		  FROM quiz_questions
		 WHERE quiz_id = $2
		 ORDER BY ordinal`, cloneID, srcQuizID,
	); err != nil {
		return 0, fmt.Errorf("clone questions:\n%w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit clone:\n%w", err)
	}
	return cloneID, nil
}
```

Handler:

```go
// AcceptShare handles POST /quizzes/shared/{token}/accept.
func (h *QuizHandler) AcceptShare(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	token := r.PathValue("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput); return
	}
	cloneID, err := h.svc.AcceptShare(r.Context(), uid, token)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"quizId": cloneID})
}
```

Route:

```go
mux.Handle("POST /quizzes/shared/{token}/accept", av(quizH.AcceptShare))
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestAcceptShare' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/share.go pkg/quiz/share_test.go api/handler/quiz_stub.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec D: POST /quizzes/shared/{token}/accept (clone-on-accept)

[+] AcceptShare: idempotent (returns existing clone) + entitlement + clone tx
[+] cloneSharedQuiz: verbatim copy of quizzes + quiz_questions
[+] handler + route
EOF
)"
```

### Task 8: `POST /quizzes/:id/send-to-friends`

Per Spec D §5.5: inserts rows into `quiz_sent_to_friends`. Notifications themselves (UI badge) are deferred — this endpoint only persists the relationship.

**Files:**
- Modify: `pkg/quiz/share.go`
- Modify: `api/handler/quiz_stub.go`
- Test: `pkg/quiz/share_test.go` (extend)

- [ ] **Step 1: Write failing tests**

```go
func TestSendToFriends_InsertsRowsForFriendsOnly(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	sender := testutil.NewVerifiedUser(t, pool)
	friend := testutil.NewVerifiedUser(t, pool)
	stranger := testutil.NewVerifiedUser(t, pool)
	testutil.NewFriendshipAccepted(t, pool, sender.ID, friend.ID)

	qid := testutil.NewQuiz(t, pool, sender.ID, 1)
	svc := quiz.NewService(pool, nil)
	share, _ := svc.CreateShare(context.Background(), sender.ID, qid, nil)

	result, err := svc.SendToFriends(context.Background(), sender.ID, qid, share.Token,
		[]int64{friend.ID, stranger.ID})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(result.Sent) != 1 || result.Sent[0] != friend.ID {
		t.Fatalf("Sent=%v, want only friend.ID", result.Sent)
	}
	if len(result.Rejected) != 1 || result.Rejected[0] != stranger.ID {
		t.Fatalf("Rejected=%v, want only stranger.ID", result.Rejected)
	}

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_sent_to_friends WHERE quiz_id=$1`, qid).Scan(&n)
	if n != 1 {
		t.Fatalf("quiz_sent_to_friends rows = %d, want 1", n)
	}
}

func TestSendToFriends_Dedup_DoesNotDoubleInsert(t *testing.T) {
	// ... call send twice with same friend ...
	// Assert: still only 1 row (PK enforces dedup).
}
```

(`testutil.NewFriendshipAccepted` inserts a `friendships` row with `status='accepted'`. Add to `testutil/seed.go`.)

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement**

Append to `pkg/quiz/share.go`:

```go
// SendResult is the response shape of SendToFriends.
type SendResult struct {
	Sent     []int64 `json:"sent"`     // recipient user_ids that received the share
	Rejected []int64 `json:"rejected"` // user_ids skipped (not accepted friends)
}

// SendToFriends records a share intent for each friendId that has an accepted
// friendship with the sender. Non-friends are silently skipped (returned in Rejected).
// The PK on quiz_sent_to_friends makes the insert dedup-safe.
func (s *Service) SendToFriends(ctx context.Context, senderID, quizID int64, shareToken string, friendIDs []int64) (SendResult, error) {
	if err := s.requireQuizOwner(ctx, senderID, quizID); err != nil {
		return SendResult{}, err
	}
	// Sanity-check token belongs to this quiz + this sender.
	if err := s.requireShareTokenForQuiz(ctx, senderID, quizID, shareToken); err != nil {
		return SendResult{}, err
	}

	// Filter recipients to accepted friends only.
	friends, err := s.acceptedFriendsAmong(ctx, senderID, friendIDs)
	if err != nil {
		return SendResult{}, err
	}
	friendSet := map[int64]bool{}
	for _, f := range friends {
		friendSet[f] = true
	}
	var rejected []int64
	for _, id := range friendIDs {
		if !friendSet[id] {
			rejected = append(rejected, id)
		}
	}

	for _, recip := range friends {
		_, err := s.db.Exec(ctx, `
			INSERT INTO quiz_sent_to_friends (quiz_id, sender_id, recipient_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (quiz_id, sender_id, recipient_id) DO NOTHING`,
			quizID, senderID, recip)
		if err != nil {
			return SendResult{}, fmt.Errorf("send to %d:\n%w", recip, err)
		}
	}
	return SendResult{Sent: friends, Rejected: rejected}, nil
}

func (s *Service) requireShareTokenForQuiz(ctx context.Context, uid, quizID int64, token string) error {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM quiz_share_links
			 WHERE token=$1 AND quiz_id=$2 AND created_by=$3 AND revoked_at IS NULL
		)`, token, quizID, uid).Scan(&ok)
	if err != nil {
		return fmt.Errorf("verify token:\n%w", err)
	}
	if !ok {
		return myErrors.ErrInvalidInput
	}
	return nil
}

// acceptedFriendsAmong returns the subset of candidateIDs that have an accepted
// friendship with uid (either direction).
func (s *Service) acceptedFriendsAmong(ctx context.Context, uid int64, candidates []int64) ([]int64, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT CASE WHEN sender_id = $1 THEN receiver_id ELSE sender_id END
		  FROM friendships
		 WHERE status='accepted'
		   AND (
		       (sender_id   = $1 AND receiver_id = ANY($2)) OR
		       (receiver_id = $1 AND sender_id   = ANY($2))
		   )`, uid, candidates)
	if err != nil {
		return nil, fmt.Errorf("friend lookup:\n%w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
```

Handler:

```go
type sendToFriendsRequest struct {
	FriendIDs  []int64 `json:"friendIds"`
	ShareToken string  `json:"shareToken"`
}

// SendToFriends handles POST /quizzes/{id}/send-to-friends.
func (h *QuizHandler) SendToFriends(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	qid, err := quizIDFromPath(r)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	var body sendToFriendsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)); return
	}
	out, err := h.svc.SendToFriends(r.Context(), uid, qid, body.ShareToken, body.FriendIDs)
	if err != nil {
		httpx.WriteError(w, err); return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

Route:

```go
mux.Handle("POST /quizzes/{id}/send-to-friends", av(quizH.SendToFriends))
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestSendToFriends' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/share.go pkg/quiz/share_test.go api/handler/quiz_stub.go cmd/app/routes.go testutil/seed.go
git commit -m "$(cat <<'EOF'
Spec D: POST /quizzes/{id}/send-to-friends

[+] SendToFriends filters recipients to accepted friendships; dedup via PK
[+] requireShareTokenForQuiz: token must belong to sender + quiz + not revoked
[+] handler + route
EOF
)"
```

---

## Phase 4 — Quality reports

### Task 9: `POST /quiz-questions/:id/report`

Pure telemetry — Spec D §5.6 explicitly says "no downstream behavior in v1".

**Files:**
- Create: `pkg/quiz/quality.go`
- Test: `pkg/quiz/quality_test.go`
- Modify: `api/handler/quiz_stub.go`

- [ ] **Step 1: Write failing tests**

```go
package quiz_test

import (
	"context"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestReportQuestion_InsertsRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)
	var questionID int64
	_ = pool.QueryRow(context.Background(),
		`SELECT id FROM quiz_questions WHERE quiz_id=$1`, qid).Scan(&questionID)

	svc := quiz.NewService(pool, nil)
	if err := svc.ReportQuestion(context.Background(), u.ID, questionID, "wrong_answer", "the AI was confidently wrong"); err != nil {
		t.Fatalf("report: %v", err)
	}

	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quiz_quality_reports WHERE question_id=$1 AND user_id=$2`,
		questionID, u.ID,
	).Scan(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestReportQuestion_InvalidReason_400(t *testing.T) {
	// ... seed user + question ...
	err := svc.ReportQuestion(ctx, u.ID, questionID, "not_in_enum", "")
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("want ErrValidation")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Expected: BUILD FAIL.

- [ ] **Step 3: Implement**

`pkg/quiz/quality.go`:

```go
package quiz

import (
	"context"
	"fmt"

	"studbud/backend/internal/myErrors"
)

// validReportReasons mirrors the SQL CHECK on quiz_quality_reports.reason.
var validReportReasons = map[string]bool{
	"wrong_answer":    true,
	"bad_distractors": true,
	"unclear":         true,
	"off_topic":       true,
	"other":           true,
}

// ReportQuestion inserts a quality-report row. Reason must be in the enum;
// note is optional. Pure telemetry; no downstream side-effects in v1.
func (s *Service) ReportQuestion(ctx context.Context, uid, questionID int64, reason, note string) error {
	if !validReportReasons[reason] {
		return fmt.Errorf("%w: reason=%q", myErrors.ErrValidation, reason)
	}
	noteVal := nullableString(note)
	if _, err := s.db.Exec(ctx, `
		INSERT INTO quiz_quality_reports (question_id, user_id, reason, note)
		VALUES ($1, $2, $3, $4)`,
		questionID, uid, reason, noteVal,
	); err != nil {
		return fmt.Errorf("insert quality report:\n%w", err)
	}
	return nil
}
```

Handler:

```go
type reportQuestionRequest struct {
	Reason string `json:"reason"`
	Note   string `json:"note,omitempty"`
}

// ReportQuestion handles POST /quiz-questions/{id}/report.
func (h *QuizHandler) ReportQuestion(w http.ResponseWriter, r *http.Request) {
	uid, err := middleware.UserIDFromCtx(r.Context())
	if err != nil {
		httpx.WriteError(w, err); return
	}
	raw := r.PathValue("id")
	qid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput); return
	}
	var body reportQuestionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)); return
	}
	if err := h.svc.ReportQuestion(r.Context(), uid, qid, body.Reason, body.Note); err != nil {
		httpx.WriteError(w, err); return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Route:

```go
mux.Handle("POST /quiz-questions/{id}/report", av(quizH.ReportQuestion))
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./pkg/quiz/ -run 'TestReportQuestion' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/quiz/quality.go pkg/quiz/quality_test.go api/handler/quiz_stub.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec D: POST /quiz-questions/{id}/report

[+] ReportQuestion enforces reason enum; note optional
[+] handler + route (204 No Content)
EOF
)"
```

---

## Phase 5 — OpenAPI

### Task 10: Document share + accept + send + report + admin

**Files:**
- Modify: `api/handler/docs_openapi.yaml`

- [ ] **Step 1: Add the path entries**

Append to the `paths:` section:

```yaml
  /quizzes/{id}/share:
    post:
      tags: [Quiz]
      summary: Create a shareable token
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      requestBody:
        required: false
        content:
          application/json:
            schema:
              type: object
              properties:
                expiresAt: { type: string, format: date-time, nullable: true }
      responses:
        '200':
          description: Token + URL
          content:
            application/json:
              schema:
                type: object
                properties:
                  token:     { type: string }
                  url:       { type: string }
                  expiresAt: { type: string, format: date-time, nullable: true }

  /quizzes/shares/{token}:
    delete:
      tags: [Quiz]
      summary: Revoke a share token (does not affect existing clones)
      parameters:
        - in: path
          name: token
          required: true
          schema: { type: string }
      responses:
        '204':
          description: Revoked
        '403': { $ref: '#/components/responses/Forbidden' }

  /quizzes/shared/{token}:
    get:
      tags: [Quiz]
      summary: Preview a shared quiz
      parameters:
        - in: path
          name: token
          required: true
          schema: { type: string }
      responses:
        '200':
          description: Preview
          content:
            application/json:
              schema:
                type: object
                properties:
                  owner:         { type: string }
                  subjectTitle:  { type: string }
                  kind:          { type: string, enum: [specific, global] }
                  questionCount: { type: integer }
                  expired:       { type: boolean }
        '410': { $ref: '#/components/responses/Gone' }

  /quizzes/shared/{token}/accept:
    post:
      tags: [Quiz]
      summary: Clone a shared quiz into the caller's account
      parameters:
        - in: path
          name: token
          required: true
          schema: { type: string }
      responses:
        '200':
          description: New quiz id
          content:
            application/json:
              schema:
                type: object
                properties:
                  quizId: { type: integer, format: int64 }
        '402': { $ref: '#/components/responses/PaymentRequired' }
        '410': { $ref: '#/components/responses/Gone' }

  /quizzes/{id}/send-to-friends:
    post:
      tags: [Quiz]
      summary: Send a share to specific friends
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [friendIds, shareToken]
              properties:
                friendIds:
                  type: array
                  items: { type: integer, format: int64 }
                shareToken: { type: string }
      responses:
        '200':
          description: Routed
          content:
            application/json:
              schema:
                type: object
                properties:
                  sent:
                    type: array
                    items: { type: integer, format: int64 }
                  rejected:
                    type: array
                    items: { type: integer, format: int64 }

  /quiz-questions/{id}/report:
    post:
      tags: [Quiz]
      summary: Report a low-quality quiz question
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: integer, format: int64 }
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [reason]
              properties:
                reason:
                  type: string
                  enum: [wrong_answer, bad_distractors, unclear, off_topic, other]
                note: { type: string }
      responses:
        '204':
          description: Recorded

  /admin/reset-quiz-demo:
    post:
      tags: [Admin]
      summary: (dev-only) Clear quiz_demo_used for a user
      parameters:
        - in: query
          name: userId
          required: true
          schema: { type: integer, format: int64 }
      responses:
        '204':
          description: Cleared
        '404':
          description: Not found (returned in production mode)
```

If `components.responses.Gone` doesn't exist, add it:

```yaml
    Gone:
      description: Resource is no longer available
      content:
        application/json:
          schema: { $ref: '#/components/schemas/Error' }
```

- [ ] **Step 2: Sanity-parse**

```
python3 -c "import yaml; yaml.safe_load(open('api/handler/docs_openapi.yaml'))"
go test ./api/handler/ -run 'TestDocs' -v
```

Expected: clean.

- [ ] **Step 3: Run the full suite + lint**

```
make test
go vet ./...
gofmt -l .
```

Expected: PASS, clean.

- [ ] **Step 4: Commit**

```bash
git add api/handler/docs_openapi.yaml
git commit -m "$(cat <<'EOF'
Spec D: OpenAPI for share/accept/send/report/admin

[+] /quizzes/{id}/share + /quizzes/shares/{token} (POST/DELETE)
[+] /quizzes/shared/{token} (GET) + .../accept (POST)
[+] /quizzes/{id}/send-to-friends
[+] /quiz-questions/{id}/report
[+] /admin/reset-quiz-demo
[+] components.responses.Gone (410)
EOF
)"
```

---

## Self-review checklist

1. **Spec coverage**
   - §3 share endpoints: Tasks 4 (POST), 5 (DELETE), 6 (GET preview).
   - §3 accept (clone): Task 7.
   - §3 send-to-friends: Task 8.
   - §3 quality report: Task 9.
   - §3 admin reset-quiz-demo: Task 3.
   - §2 invariants: clone independence (revoke does not touch clones) — verified in Task 7's `TestAcceptShare_RevokedAfter_AlreadyTakenStillWorks`.
   - §2 invariants: at-most-one demo per user — verified in Tasks 1 + 2.
   - §5.5 flow: Tasks 4-8 chained.
   - §5.6 flow: Task 9.
   - §1 architecture: no new AI calls; sharing is a pure DB path. ✓
2. **Cross-spec deferrals**
   - In-app notification UI: deliberately deferred. `quiz_sent_to_friends` is the only persisted side-effect; a future plan can read it.
   - Frontend share dialog / accept page: out of scope per the project's backend-only repo.
3. **Type consistency**
   - `Share`, `SharePreviewResult`, `SendResult` all use `int64` for ids consistently.
   - `validReportReasons` map mirrors the SQL CHECK 1:1.
   - `ErrGone` mapping verified in Task 6's httpx step.
4. **No placeholders** — every step has SQL + Go body.
5. **CLAUDE.md fit** — methods stay short; one file per concern (`share.go`, `quality.go`, `demo.go`, `admin_quiz.go`).

---

## Execution handoff

When complete:
- `make test` clean.
- `go vet ./...` clean.
- ~10 commits on `feat/spec-d-ai-quiz` matching task headings.
- The branch should already carry Plans D1 + (optionally) D2's commits. PR options:
  - One PR per plan (D1 → review → merge → D2 → … → D3) if the team prefers small PRs.
  - One bundled PR for all three plans if D2 + D3 are short enough not to lose review attention.
  - D3 can be split further: "share + accept" is the load-bearing half; "quality + admin + demo" are independent and could ship as a follow-up if D3 grows.
