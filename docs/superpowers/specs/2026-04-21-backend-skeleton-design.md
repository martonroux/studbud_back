# StudBud Backend Skeleton — Design

**Date:** 2026-04-21
**Status:** Approved (ready for implementation plan)
**Depends on:** —
**Owns downstream:** Every feature spec (A, B.0, B, C, D, E) plugs into this skeleton without restructuring it.

---

## 1. Purpose

Build the backend skeleton for StudBud so that:

1. The full DB schema is created on first boot — no future migration scripts ever needed.
2. Every spec'd route (current + future) is wired from day one, returning `501 not_implemented` for unimplemented features.
3. Every service is a real Go type with real method signatures; stub bodies return `ErrNotImplemented`.
4. Adding a feature later = flipping stub method bodies + filling empty internal packages. Zero router/DB/deps refactor.
5. Full test scaffolding exists from day one (real Postgres, `testutil/` fixtures, one end-to-end example test).

Everything in this doc is derived from `CLAUDE.md`, `API.md`, `PROJECT_DESCRIPTION.md`, and specs A, B.0, B, C, D, E. Cross-spec constraints from the roadmap (`2026-04-19-specs-roadmap.md`) are honoured.

---

## 2. Directory Layout

```
study_buddy_backend/
├── cmd/
│   └── app/
│       ├── main.go              # boot: config → db → deps → routes → serve
│       ├── deps.go              # AppDeps struct + constructor
│       └── routes.go            # all route registrations in one place
├── api/
│   ├── handler/                 # thin HTTP adapters, one file per domain
│   │   ├── user.go
│   │   ├── email_verification.go
│   │   ├── image.go
│   │   ├── subject.go
│   │   ├── chapter.go
│   │   ├── flashcard.go
│   │   ├── search.go
│   │   ├── friendship.go
│   │   ├── subject_subscription.go
│   │   ├── collaboration.go
│   │   ├── preferences.go
│   │   ├── gamification.go
│   │   ├── achievement.go
│   │   ├── ai.go                # stub routes for Spec A
│   │   ├── billing.go           # stub routes for Spec C
│   │   ├── exam.go              # stub routes for Spec B
│   │   ├── quiz.go              # stub routes for Spec D
│   │   ├── duel.go              # stub routes for Spec E
│   │   ├── user_report.go       # stub routes for Spec E
│   │   └── admin.go
│   └── service/                 # (thin seam if a handler composes many services)
├── pkg/                         # domain packages — models + services + SQL
│   ├── access/                  # AccessService: HasAIAccess (entitlement gate)
│   ├── user/
│   ├── emailverification/
│   ├── image/
│   ├── subject/
│   ├── chapter/
│   ├── flashcard/
│   ├── search/
│   ├── friendship/
│   ├── subjectsubscription/
│   ├── collaboration/
│   ├── preferences/
│   ├── gamification/
│   ├── achievement/
│   ├── ai/                      # stub service, real signatures
│   ├── aiquota/                 # stub service, real signatures
│   ├── keywordextraction/       # stub service, real signatures
│   ├── exam/                    # stub service
│   ├── revisionplan/            # stub service
│   ├── crosssubjectshortlist/   # stub service
│   ├── billing/                 # stub service
│   ├── quiz/                    # stub service
│   ├── duel/                    # stub service
│   ├── userreport/              # stub service
│   └── admin/
├── internal/                    # infra, not domain
│   ├── config/                  # env loader + Config struct
│   ├── db/                      # pgx pool wrapper + tx helper
│   ├── jwt/                     # sign/verify
│   ├── email/                   # SMTP sender interface + smtpSender
│   ├── storage/                 # filesystem image writer + id gen
│   ├── myErrors/                # sentinel errors + AppError
│   ├── httpx/                   # DecodeJSON, WriteJSON, WriteError, SSE stream helper
│   ├── http/middleware/         # recoverer, requestid, cors, logger, auth, verified, admin
│   ├── authctx/                 # typed ctx key for uid
│   ├── cron/                    # ticker-based scheduler + registered jobs
│   ├── aiProvider/              # empty (noopClient now; real in Spec A)
│   ├── keywordWorker/           # empty (no-op Start; real in Spec B.0)
│   ├── billing/                 # empty (noopClient; real in Spec C)
│   └── duelHub/                 # empty (stateless WS hub; real in Spec E)
├── db_sql/                      # schema setup — one file per domain
│   ├── setup.go                 # SetupAll orchestrator
│   ├── setup_core.go
│   ├── setup_ai.go
│   ├── setup_billing.go
│   ├── setup_plan.go
│   ├── setup_quiz.go
│   └── setup_duel.go
├── testutil/                    # shared test helpers
│   ├── testdb.go
│   ├── fixtures.go
│   ├── email.go
│   ├── stripe.go
│   ├── ai.go
│   └── clock.go
├── uploads/                     # filesystem image store (gitignored except .keep)
├── .env.example
├── launch_app.sh
├── setup_db.sh
├── Makefile
├── go.mod                       # module: studbud/backend
└── go.sum
```

**Key rules:**
- `api/handler` never imports another `pkg/<domain>` directly — only through services it already holds.
- `pkg/<domain>` never imports `api/`.
- `internal/` is the only place that touches external infra (SMTP, filesystem, Stripe SDK, Anthropic SDK, pgx driver).
- `db_sql/` is stateless: every DDL is `CREATE TABLE IF NOT EXISTS` / `CREATE OR REPLACE FUNCTION` / `CREATE INDEX IF NOT EXISTS` / `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`. Run on every boot, always idempotent.

---

## 3. Complete DB Schema

Every table from every spec, created on first boot. Ordering of `setup_*.go` files matters only for FK resolution: core → ai → billing → plan → quiz → duel.

### 3.1 setup_core.go

```sql
CREATE TABLE IF NOT EXISTS users (
    id                        BIGSERIAL PRIMARY KEY,
    email                     TEXT NOT NULL UNIQUE,
    password_hash             TEXT NOT NULL,
    display_name              TEXT NOT NULL,
    email_verified            BOOLEAN NOT NULL DEFAULT false,
    verified_at               TIMESTAMPTZ NULL,
    profile_picture_image_id  TEXT NULL,
    stripe_customer_id        TEXT UNIQUE NULL,
    is_admin                  BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verification_throttle (
    user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_sent   TIMESTAMPTZ NOT NULL DEFAULT now(),
    send_count  INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS images (
    id         TEXT PRIMARY KEY,                -- "abcd_efgh"
    owner_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename   TEXT NOT NULL,                   -- on-disk filename
    mime_type  TEXT NOT NULL,
    bytes      BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
  ADD CONSTRAINT users_profile_pic_fk
  FOREIGN KEY (profile_picture_image_id) REFERENCES images(id) ON DELETE SET NULL;
-- wrapped in DO block with IF NOT EXISTS check, idempotent.

CREATE TABLE IF NOT EXISTS subjects (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search_vec  tsvector
);

CREATE INDEX IF NOT EXISTS idx_subjects_search ON subjects USING GIN (search_vec);
CREATE INDEX IF NOT EXISTS idx_subjects_owner ON subjects(owner_id);

-- trigger to maintain subjects.search_vec
CREATE OR REPLACE FUNCTION subjects_search_vec_update() RETURNS trigger AS $$
BEGIN
  NEW.search_vec :=
    setweight(to_tsvector('simple', coalesce(NEW.name,'')), 'A') ||
    setweight(to_tsvector('simple', coalesce(NEW.description,'')), 'B');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_subjects_search_vec ON subjects;
CREATE TRIGGER trg_subjects_search_vec
  BEFORE INSERT OR UPDATE ON subjects
  FOR EACH ROW EXECUTE FUNCTION subjects_search_vec_update();

CREATE TABLE IF NOT EXISTS chapters (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chapters_subject ON chapters(subject_id);

CREATE TABLE IF NOT EXISTS flashcards (
    id            BIGSERIAL PRIMARY KEY,
    chapter_id    BIGINT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    question      TEXT NOT NULL,
    answer        TEXT NOT NULL,
    image_id      TEXT NULL REFERENCES images(id) ON DELETE SET NULL,
    source        TEXT NOT NULL DEFAULT 'manual',        -- 'manual' | 'ai'
    due_at        TIMESTAMPTZ NULL,
    last_result   SMALLINT NULL,                          -- -1 fail, 0 hesit, 1 good
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_flashcards_chapter ON flashcards(chapter_id);

CREATE TABLE IF NOT EXISTS friendships (
    id            BIGSERIAL PRIMARY KEY,
    requester_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    addressee_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status        TEXT NOT NULL,                          -- 'pending' | 'accepted' | 'blocked'
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT friendships_pair_chk CHECK (requester_id <> addressee_id),
    UNIQUE (requester_id, addressee_id)
);

CREATE TABLE IF NOT EXISTS subject_subscriptions (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, subject_id)
);

CREATE TABLE IF NOT EXISTS collaborators (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,                              -- 'viewer' | 'editor'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subject_id, user_id)
);

CREATE TABLE IF NOT EXISTS invite_links (
    id          BIGSERIAL PRIMARY KEY,
    subject_id  BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NULL,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS preferences (
    user_id             BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    theme               TEXT NOT NULL DEFAULT 'system',
    language            TEXT NOT NULL DEFAULT 'en',
    sound_enabled       BOOLEAN NOT NULL DEFAULT true,
    haptics_enabled     BOOLEAN NOT NULL DEFAULT true,
    daily_reminder      BOOLEAN NOT NULL DEFAULT false,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS streaks (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    current_days    INT NOT NULL DEFAULT 0,
    longest_days    INT NOT NULL DEFAULT 0,
    last_active_day DATE NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS daily_goals (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day        DATE NOT NULL,
    goal_cards INT NOT NULL,
    done_cards INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS training_sessions (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id      BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    chapter_id      BIGINT NULL REFERENCES chapters(id) ON DELETE SET NULL,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at        TIMESTAMPTZ NULL,
    cards_seen      INT NOT NULL DEFAULT 0,
    cards_good      INT NOT NULL DEFAULT 0,
    cards_hesitant  INT NOT NULL DEFAULT 0,
    cards_failed    INT NOT NULL DEFAULT 0,
    duration_ms     BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_session_bests (
    user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    best_accuracy NUMERIC(5,2) NOT NULL DEFAULT 0,
    best_cards   INT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS unlocked_achievements (
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_key TEXT NOT NULL,
    unlocked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, achievement_key)
);
```

### 3.2 setup_ai.go

```sql
CREATE TABLE IF NOT EXISTS ai_jobs (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature_key   TEXT NOT NULL,                 -- 'generate_prompt' | 'generate_pdf' | 'check_flashcard' | 'revision_plan' | 'cross_subject_rank' | 'quiz' | 'extract_keywords'
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cents_spent   INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL,                 -- 'running' | 'succeeded' | 'failed'
    error         TEXT NULL,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_jobs_user_day ON ai_jobs(user_id, started_at);

CREATE TABLE IF NOT EXISTS ai_quota_daily (
    user_id                   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day                       DATE NOT NULL,
    prompt_calls              INT NOT NULL DEFAULT 0,
    pdf_calls                 INT NOT NULL DEFAULT 0,
    pdf_pages                 INT NOT NULL DEFAULT 0,
    check_calls               INT NOT NULL DEFAULT 0,
    plan_calls                INT NOT NULL DEFAULT 0,
    cross_subject_rank_calls  INT NOT NULL DEFAULT 0,
    quiz_calls                INT NOT NULL DEFAULT 0,
    quiz_demo_used            BOOLEAN NOT NULL DEFAULT false,
    extract_keywords_calls    INT NOT NULL DEFAULT 0,
    cents_spent               INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS ai_extraction_jobs (
    id            BIGSERIAL PRIMARY KEY,
    flashcard_id  BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending',  -- 'pending' | 'claimed' | 'succeeded' | 'failed'
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT NULL,
    claimed_at    TIMESTAMPTZ NULL,
    finished_at   TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flashcard_id)
);
CREATE INDEX IF NOT EXISTS idx_ai_extraction_jobs_status ON ai_extraction_jobs(status);

CREATE TABLE IF NOT EXISTS flashcard_keywords (
    flashcard_id BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword      TEXT NOT NULL,
    weight       REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (flashcard_id, keyword)
);
CREATE INDEX IF NOT EXISTS idx_flashcard_keywords_kw ON flashcard_keywords(keyword);
```

### 3.3 setup_billing.go

```sql
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id                        BIGSERIAL PRIMARY KEY,
    user_id                   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_subscription_id    TEXT UNIQUE NULL,
    plan                      TEXT NOT NULL,         -- 'pro_monthly' | 'pro_annual' | 'comp'
    status                    TEXT NOT NULL,         -- 'active' | 'past_due' | 'canceled' | 'trialing' | 'comp'
    current_period_end        TIMESTAMPTZ NULL,
    cancel_at_period_end      BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_user_subs_user ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subs_status ON user_subscriptions(status);

CREATE TABLE IF NOT EXISTS billing_events (
    id              BIGSERIAL PRIMARY KEY,
    stripe_event_id TEXT UNIQUE NOT NULL,
    type            TEXT NOT NULL,
    user_id         BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    payload         JSONB NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ NULL,
    error           TEXT NULL
);

CREATE OR REPLACE FUNCTION user_has_ai_access(uid BIGINT) RETURNS BOOLEAN AS $$
DECLARE
  ok BOOLEAN;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM user_subscriptions
    WHERE user_id = uid
      AND (
        status = 'comp' OR
        (status IN ('active','trialing') AND
         (current_period_end IS NULL OR current_period_end > now()))
      )
  ) INTO ok;
  RETURN ok;
END;
$$ LANGUAGE plpgsql STABLE;
```

### 3.4 setup_plan.go

```sql
CREATE TABLE IF NOT EXISTS exams (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id        BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    exam_date         DATE NOT NULL,
    annales_image_id  TEXT NULL REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exams_user_date ON exams(user_id, exam_date);

CREATE TABLE IF NOT EXISTS revision_plans (
    id                 BIGSERIAL PRIMARY KEY,
    exam_id            BIGINT NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    intensity          TEXT NOT NULL DEFAULT 'balanced',   -- 'light' | 'balanced' | 'intense'
    payload            JSONB NOT NULL,                     -- materialized per-day plan
    generated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS revision_plan_progress (
    plan_id     BIGINT NOT NULL REFERENCES revision_plans(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    item_key    TEXT NOT NULL,                              -- stable id inside payload
    done_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, day, item_key)
);
```

### 3.5 setup_quiz.go

```sql
CREATE TABLE IF NOT EXISTS quizzes (
    id            BIGSERIAL PRIMARY KEY,
    owner_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id    BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    title         TEXT NOT NULL,
    kind          TEXT NOT NULL,                            -- 'specific' | 'global'
    source        TEXT NOT NULL,                            -- 'user' | 'plan' | 'shared_copy' | 'duel'
    parent_quiz_id BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    plan_id       BIGINT NULL REFERENCES revision_plans(id) ON DELETE SET NULL,
    duel_id       BIGINT NULL,                              -- FK added after duels table exists
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quizzes_source_check CHECK (source IN ('user','plan','shared_copy','duel')),
    CONSTRAINT quizzes_kind_check   CHECK (kind IN ('specific','global'))
);
CREATE INDEX IF NOT EXISTS idx_quizzes_owner ON quizzes(owner_id);

CREATE TABLE IF NOT EXISTS quiz_questions (
    id             BIGSERIAL PRIMARY KEY,
    quiz_id        BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    position       INT NOT NULL,
    prompt         TEXT NOT NULL,
    choices        JSONB NOT NULL,                          -- ["A","B","C","D"]
    correct_index  SMALLINT NOT NULL,
    source_flashcard_id BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL,
    explanation    TEXT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_quiz_questions_quiz ON quiz_questions(quiz_id);

CREATE TABLE IF NOT EXISTS quiz_attempts (
    id          BIGSERIAL PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ NULL,
    score       INT NULL,
    total       INT NULL
);

CREATE TABLE IF NOT EXISTS quiz_attempt_answers (
    attempt_id     BIGINT NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id    BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    chosen_index   SMALLINT NOT NULL,
    is_correct     BOOLEAN NOT NULL,
    answered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attempt_id, question_id)
);

CREATE TABLE IF NOT EXISTS quiz_share_links (
    id          BIGSERIAL PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quiz_sent_to_friends (
    quiz_id    BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);

CREATE TABLE IF NOT EXISTS quiz_quality_reports (
    id          BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 3.6 setup_duel.go

```sql
CREATE TABLE IF NOT EXISTS duels (
    id                     BIGSERIAL PRIMARY KEY,
    challenger_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenger_name        TEXT NOT NULL,                    -- snapshot
    invitee_id             BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    invitee_name           TEXT NULL,                        -- snapshot
    subject_id             BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    subject_name           TEXT NOT NULL,                    -- snapshot
    status                 TEXT NOT NULL,                    -- 'waiting' | 'accepted' | 'active' | 'finished' | 'canceled'
    challenger_score       INT NOT NULL DEFAULT 0,
    invitee_score          INT NOT NULL DEFAULT 0,
    current_round          INT NOT NULL DEFAULT 0,
    rounds_total           INT NOT NULL DEFAULT 5,
    quiz_id                BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at             TIMESTAMPTZ NULL,
    finished_at            TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_duels_challenger ON duels(challenger_id);
CREATE INDEX IF NOT EXISTS idx_duels_invitee ON duels(invitee_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_duels_one_waiting_per_challenger
  ON duels(challenger_id) WHERE status = 'waiting';

-- backfill quizzes.duel_id FK now that duels exists
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'quizzes_duel_id_fkey'
  ) THEN
    ALTER TABLE quizzes
      ADD CONSTRAINT quizzes_duel_id_fkey
      FOREIGN KEY (duel_id) REFERENCES duels(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS duel_invite_tokens (
    id         BIGSERIAL PRIMARY KEY,
    duel_id    BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS duel_round_questions (
    duel_id     BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no    INT NOT NULL,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    PRIMARY KEY (duel_id, round_no)
);

CREATE TABLE IF NOT EXISTS duel_round_answers (
    duel_id       BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no      INT NOT NULL,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chosen_index  SMALLINT NOT NULL,
    is_correct    BOOLEAN NOT NULL,
    server_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (duel_id, round_no, user_id)
);

CREATE TABLE IF NOT EXISTS duel_user_stats (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    duels_played   INT NOT NULL DEFAULT 0,
    duels_won      INT NOT NULL DEFAULT 0,
    duels_lost     INT NOT NULL DEFAULT 0,
    duels_drawn    INT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS duel_head_to_head (
    user_a_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    a_wins       INT NOT NULL DEFAULT 0,
    b_wins       INT NOT NULL DEFAULT 0,
    draws        INT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT h2h_order_chk CHECK (user_a_id < user_b_id),
    PRIMARY KEY (user_a_id, user_b_id)
);

CREATE TABLE IF NOT EXISTS user_reports (
    id         BIGSERIAL PRIMARY KEY,
    reporter_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason     TEXT NOT NULL,
    context    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 3.7 SetupAll orchestrator

`db_sql/setup.go` runs all setup files in order, each inside its own transaction. Any failure aborts boot with a clear message.

```go
func SetupAll(ctx context.Context, pool *pgxpool.Pool) error {
    steps := []struct{
        name string
        fn   func(context.Context, *pgxpool.Pool) error
    }{
        {"core",    setupCore},
        {"ai",      setupAI},
        {"billing", setupBilling},
        {"plan",    setupPlan},
        {"quiz",    setupQuiz},
        {"duel",    setupDuel},
    }
    for _, s := range steps {
        if err := s.fn(ctx, pool); err != nil {
            return fmt.Errorf("setup %s: %w", s.name, err)
        }
    }
    return nil
}
```

---

## 4. Cross-Cutting Type Decisions

- **Image IDs:** `TEXT`, 8 lowercase-alphanumeric chars in `"aaaa_bbbb"` form. Generated by `internal/storage/id.go`. Matches API.md.
- **All other IDs:** `BIGSERIAL`.
- **Timestamps:** `TIMESTAMPTZ NOT NULL DEFAULT now()` for all `created_at` / `updated_at`.
- **Entitlement:** `SELECT user_has_ai_access($1)` via `AccessService.HasAIAccess`. Single source of truth. No `ai_subscription_active` column.
- **AI quota:** Every counter column exists on `ai_quota_daily` from day one. `AiQuotaService.Debit(ctx, uid, feature)` routes to a column. No ALTER TABLE ever.
- **Search:** tsvector on `subjects` only (GIN index + trigger). Chapters/flashcards reached via nav, not global search.
- **`users` table:** Carries `stripe_customer_id`, `email_verified`, `is_admin`, `profile_picture_image_id` from day one.

---

## 5. Service Layer

**Uniform constructor pattern:**
```go
type SubjectService struct { db *pgxpool.Pool }
func NewSubjectService(db *pgxpool.Pool) *SubjectService { return &SubjectService{db: db} }
```
No interfaces unless two implementations exist.

**Service package contents (`pkg/<domain>/`):**
- `model.go` — DB row structs + DTOs.
- `service.go` — methods; split `service_read.go` / `service_write.go` beyond ~300 lines.
- `queries.go` — raw SQL as `const` strings.
- `errors.go` — domain sentinel errors.

**`AccessService` is the single entitlement gate.** Every feature uses only `HasAIAccess`. Future gates (e.g., `CanCreateSubject`) added here.

**Stub services.** Every spec'd service exists as a real struct with real method signatures. Bodies return `myErrors.ErrNotImplemented`. Example:
```go
func (s *QuizService) CreateSpecific(ctx context.Context, uid int64, in CreateSpecificInput) (*Quiz, error) {
    return nil, myErrors.ErrNotImplemented
}
```
This lets routes wire today and feature implementation later = replace body only.

**Shared collaborators via constructor injection.**
- `AiService(db, access, quota, provider)` — provider is `aiProvider.Client` (noopClient until Spec A).
- `QuizService(db, ai, access, quota, flashcards)`.
- `RevisionPlanService(db, ai, exam, quota)`.
- `DuelService(db, quiz, access, hub)` — hub is `duelHub.Hub` (no-op until Spec E).
- `BillingService(db, access, client)` — client is `billing.Client` (noopClient until Spec C).

---

## 6. Router & Middleware

**Go 1.22 enhanced `ServeMux`.** Method+path patterns, `r.PathValue("id")` for path vars. No third-party router.

**Middleware chain:** `Recoverer → RequestID → CORS → Logger → [Auth] → [RequireVerified] → Handler`.

**Three route groups in `cmd/app/routes.go`:**
- **Public:** `/user-register`, `/user-login`, `/verify-email`, `/resend-verification`, `/billing/webhook` (webhook auth is Stripe signature).
- **Authed (no verification):** `/user-test-jwt`.
- **Verified (default):** everything else — subjects, chapters, flashcards, search, friendships, collaborators, preferences, gamification, `ai/*`, `billing/*` (non-webhook), `exams/*`, `quizzes/*`, `duels/*`, `admin/*`, `ws/duels/{id}`.

**Admin gate.** `RequireAdmin` middleware checks `users.is_admin`. Column lives in `setup_core.go`.

**WebSocket.** `/ws/duels/{id}` uses a short-lived WS ticket issued via REST (JWT doesn't ride cleanly through browser WS handshake). Ticket service is a stub; real impl in Spec E.

**SSE.** `/ai/generate-flashcards`, `/exams/:id/generate-plan`, quiz generation, etc. use `httpx.Stream(w, chunks)` helper — real infra, usable day one.

**Thin handler pattern (≤ 15 lines):**
```go
func (h *SubjectHandler) Create(w http.ResponseWriter, r *http.Request) {
    uid := authctx.UID(r.Context())
    var in subject.CreateInput
    if err := httpx.DecodeJSON(r, &in); err != nil { httpx.WriteError(w, err); return }
    out, err := h.svc.Create(r.Context(), uid, in)
    if err != nil { httpx.WriteError(w, err); return }
    httpx.WriteJSON(w, http.StatusOK, out)
}
```

---

## 7. Errors

**Package: `internal/myErrors/`.**

**Sentinels:**
```go
ErrNotFound, ErrUnauthenticated, ErrNotVerified, ErrForbidden, ErrAdminRequired,
ErrInvalidInput, ErrValidation, ErrConflict, ErrAlreadyVerified,
ErrNoAIAccess, ErrQuotaExhausted, ErrPdfTooLarge,
ErrAIProvider, ErrStripe,
ErrNotImplemented
```

**`AppError`** carries context (Code, Message, Field, Status, Wrapped). Unwraps to the sentinel. `Status != 0` overrides status mapping.

**Status mapping (in `httpx.WriteError`):**

| Error | HTTP |
|---|---|
| `ErrUnauthenticated` | 401 |
| `ErrNotVerified`, `ErrForbidden`, `ErrAdminRequired` | 403 |
| `ErrNotFound` | 404 |
| `ErrValidation`, `ErrInvalidInput` | 400 |
| `ErrConflict`, `ErrAlreadyVerified` | 409 |
| `ErrNoAIAccess` | 402 |
| `ErrQuotaExhausted`, `ErrPdfTooLarge` | 429 |
| `ErrAIProvider`, `ErrStripe` | 502 |
| `ErrNotImplemented` | 501 |
| default | 500 |

**Response shape:** `{"error":{"code":"...","message":"...","field":null}}`. `code` derives from the sentinel name when `AppError.Code` is empty.

**Logging:** Recoverer logs 5xx with wrap chain + request ID; 4xx at info level.

---

## 8. Config & Boot

**`internal/config/`** — one `Config` struct, one `Load()`. Validation at boot, fatal on failure.

**Config fields:**
- Runtime: `Port`, `Env` (`dev|test|prod`), `FrontendURL`, `BackendURL`.
- DB: `DatabaseURL`.
- Auth: `JWTSecret` (≥ 32 bytes), `JWTIssuer`, `JWTTTL`.
- Email: `SMTPHost`, `SMTPPort`, `SMTPUser`, `SMTPPass`, `SMTPFrom`.
- Storage: `UploadDir`.
- AI: `AnthropicAPIKey`, `AIModel`.
- Billing: `StripeMode`, `StripeSecretKey`, `StripeWebhookSecret`, `StripePriceProMonth`, `StripePriceProAnnual`.
- Ops: `AdminBootstrapEmail`.

**Validation rules:**
- Required always: DB, JWT, Frontend/Backend URLs, SMTP.
- Required in prod only: AI key, Stripe live keys.
- Rejected: `StripeMode=live` when `Env != prod`.

**Boot sequence (`cmd/app/main.go`):**
```
config.Load → pgxpool.New → dbsql.SetupAll → buildDeps →
routes.Register → cron.Start → http.Server.ListenAndServe
```

**Test env isolation:** `Env=test` forces `DATABASE_URL` to end in `/studbud_test`; swaps email sender and Stripe client for test doubles.

**Admin bootstrap:** On boot, `AdminBootstrapEmail` promotes matching user to `is_admin=true`. No-op otherwise.

**Cron (`internal/cron/`):** ticker per job. Day-one: verification throttle GC, `ai_quota_daily` GC (rows older than 30 days at 04:00 UTC), `ai_extraction_jobs` stale-job reclaim. Stubs registered with real cadence, no-op bodies until their feature ships.

---

## 9. Testing

**Real Postgres. No mocks.** DB: `studbud_test`. Enforced by `testutil.MustTestEnv()`.

**`testutil/` package (repo root):**
- `testdb.go` — `OpenTestDB(t)` once per `TestMain` + `Reset(t, db)` (one `TRUNCATE ... RESTART IDENTITY CASCADE` of all tables).
- `fixtures.go` — `NewUser`, `NewVerifiedUser`, `NewSubject`, `NewChapter`, `NewFlashcard`, `NewFlashcardWithKeywords`, `GiveAIAccess`, `ExhaustQuota`.
- `email.go` — in-memory `email.Sender` recorder.
- `stripe.go` — fake `billing.Client`.
- `ai.go` — fake `aiProvider.Client` with canned chunks.
- `clock.go` — fake clock for quota reset / JWT expiry tests.

**Test commands:** `make test` → `go test ./... -p 1 -count=1`. `-p 1` because tests share one DB.

**File conventions:**
- `pkg/<domain>/service_test.go` — service against real DB.
- `api/handler/<domain>_test.go` — integration via `httptest.NewServer(routes.Register(deps))`.

**One committed example:** `pkg/user/service_test.go` — `Register → VerifyEmail → Login` round-trip. Proves DB, JWT, email recorder, and handler chain are all hooked up.

**Local setup:** `setup_db.sh` at repo root creates `studbud` and `studbud_test` if missing. No Docker required.

**CI:** GitHub Actions workflow stub — Postgres service container, `make test`. Lint job empty (future `golangci-lint`).

---

## 10. What the Skeleton Ships vs Defers

### Ships as real, working code
- Full DB schema (all tables, indexes, triggers, SQL functions from every spec).
- User register + email verification + login + JWT.
- Images upload / serve / delete.
- Subject + Chapter + Flashcard CRUD (manual, no AI).
- Subject search (tsvector).
- Friendships, subject subscriptions, collaborators, invite links.
- Preferences, daily goal, streaks, training sessions, achievements — all server-authoritative (covers roadmap §F).
- `AccessService.HasAIAccess` wired (returns `false` until Spec C seeds subs — plumbing works).
- Admin routes gated by `is_admin`.
- Cron scaffold + verification throttle GC job.
- Full `testutil/` + one end-to-end example test.
- `httpx.Stream` SSE helper.

### Deferred to owning spec's impl plan (present as stubs)
- `internal/aiProvider/` → real Anthropic client (Spec A).
- `internal/keywordWorker/` → real extraction loop (Spec B.0).
- `pkg/exam`, `pkg/revisionplan`, `pkg/crosssubjectshortlist` → real bodies (Spec B).
- `internal/billing/` → real Stripe client + webhook verification (Spec C).
- `pkg/quiz` → real bodies (Spec D).
- `internal/duelHub/`, `pkg/duel`, `pkg/userreport` → real bodies (Spec E).

### Never in skeleton, not yet in any spec
- Full SRS algorithm, offline mode, push notifications, analytics, OCR, export/import.

---

## 11. Acceptance Criteria

Skeleton is "done" when:
1. `go vet ./... && go build ./...` clean.
2. `./launch_app.sh` boots against an empty Postgres, creates every table on first boot, idempotent on subsequent boots.
3. `make test` passes (includes the `Register → VerifyEmail → Login` round-trip).
4. `curl /user-register` + `/verify-email` + `/user-login` + `/subject-create` works end-to-end.
5. Every spec'd future route responds `501 {"error":{"code":"not_implemented",...}}` — not 404.
6. `AccessService.HasAIAccess(ctx, uid)` returns `false` for unsubscribed users (via `SELECT user_has_ai_access($1)`).

---

## 12. What Subsequent Per-Feature Plans Look Like

Every feature's implementation plan follows the same shape:
1. Flip stub method bodies on the relevant service.
2. Wire real collaborators in the matching `internal/` package.
3. Add tests using existing `testutil/` fixtures.
4. Zero schema changes. Zero router restructure. Zero `AppDeps` refactor.

If a future feature genuinely needs a new column we missed, it's a one-line `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in the right `setup_*.go`. Still idempotent. Still no migration tool.

---

## 13. Locked Cross-Spec Constraints Honoured

From the roadmap:
- All AI calls route through `RunStructuredGeneration` (Spec A §3.2) — `AiService` is the only place that imports `aiProvider`.
- Entitlement + quota checked inside the pipeline — `AiService` composes `AccessService` + `AiQuotaService`.
- Frontend never talks to provider — no API keys ever leave server.
- `ai_subscription_active` stub skipped — go straight to Spec C's `user_subscriptions` + `user_has_ai_access()`.
- Keyword index is system-side cost — `flashcard_keywords` has no user quota.
- B.0 has no HTTP endpoints — no handler for keyword extraction.
- One exam = one subject — `exams.subject_id NOT NULL`, no join table.
- Plan quota separate — `ai_quota_daily.plan_calls` distinct column.
- Annales debit Spec A's `pdf_pages` — no parallel counter.
- Quiz kinds `specific` | `global` — CHECK constraint.
- Plan-materialized quizzes skip quiz quota — handled in `QuizService.CreateForPlan` (stub signature).
- Quiz questions immutable post-generation — no UPDATE methods on `quiz_questions`.
- Shared-copy quizzes independent — clone via INSERT, no FK ties them.
- Quiz share is non-competitive — duels live separately.
- Head-to-head scoring server-authoritative — `duel_round_answers.server_received_at`.
- WebSocket hubs stateless — `duelHub.Hub` reads state from DB on boot.
- Snapshot social context on creation — `duels.challenger_name`, `duels.invitee_name`, `duels.subject_name`.
- Challenger-pays — encoded in `DuelService.Create` (stub signature).
