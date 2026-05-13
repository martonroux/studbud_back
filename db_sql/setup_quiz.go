package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// quizSchema installs the Spec D §2 quiz tables. Pre-launch we destructively
// realign the scaffold; once the codebase has shipped this can move to pure
// CREATE TABLE IF NOT EXISTS + ALTER. The DROP ordering respects FKs (children
// before parents).
const quizSchema = `
DROP TABLE IF EXISTS quiz_quality_reports CASCADE;
DROP TABLE IF EXISTS quiz_sent_to_friends CASCADE;
DROP TABLE IF EXISTS quiz_share_links CASCADE;
DROP TABLE IF EXISTS quiz_attempt_answers CASCADE;
DROP TABLE IF EXISTS quiz_attempts CASCADE;
DROP TABLE IF EXISTS quiz_questions CASCADE;
DROP TABLE IF EXISTS quizzes CASCADE;

CREATE TABLE quizzes (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id          BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    chapter_id          BIGINT REFERENCES chapters(id) ON DELETE SET NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('specific','global')),
    source              TEXT NOT NULL CHECK (source IN ('user','plan','shared_copy')),
    source_plan_id      BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    source_share_token  TEXT,
    card_pool_jsonb     JSONB NOT NULL,
    settings_jsonb      JSONB NOT NULL,
    question_count      INT NOT NULL,
    model               TEXT NOT NULL,
    prompt_hash         TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quizzes_user_created  ON quizzes (user_id, created_at DESC);
CREATE INDEX idx_quizzes_subject       ON quizzes (subject_id);

CREATE TABLE quiz_questions (
    id                       BIGSERIAL PRIMARY KEY,
    quiz_id                  BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    ordinal                  INT NOT NULL,
    question_type            TEXT NOT NULL CHECK (question_type IN ('multi_choice','true_false','fill_blank')),
    stem                     TEXT NOT NULL,
    options_jsonb            JSONB,
    correct_jsonb            JSONB NOT NULL,
    explanation              TEXT,
    referenced_fc_ids_jsonb  JSONB NOT NULL,
    UNIQUE (quiz_id, ordinal)
);
CREATE INDEX idx_quiz_questions_quiz ON quiz_questions (quiz_id);

CREATE TABLE quiz_attempts (
    id            BIGSERIAL PRIMARY KEY,
    quiz_id       BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state         TEXT NOT NULL CHECK (state IN ('in_progress','completed','abandoned')),
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ,
    correct_count INT NOT NULL DEFAULT 0,
    total_count   INT NOT NULL,
    score_pct     INT,
    plan_id       BIGINT REFERENCES revision_plans(id) ON DELETE SET NULL,
    plan_date     DATE
);
CREATE INDEX idx_quiz_attempts_user_started ON quiz_attempts (user_id, started_at DESC);
CREATE INDEX idx_quiz_attempts_quiz_state   ON quiz_attempts (quiz_id, state);
CREATE UNIQUE INDEX uniq_quiz_attempts_in_progress
    ON quiz_attempts (quiz_id, user_id) WHERE state = 'in_progress';

CREATE TABLE quiz_attempt_answers (
    attempt_id        BIGINT NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id       BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_answer_jsonb JSONB NOT NULL,
    correct           BOOLEAN NOT NULL,
    answered_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attempt_id, question_id)
);

CREATE TABLE quiz_share_links (
    token       TEXT PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_quiz_share_links_quiz ON quiz_share_links (quiz_id);

CREATE TABLE quiz_sent_to_friends (
    quiz_id      BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);

CREATE TABLE quiz_quality_reports (
    id          BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL CHECK (reason IN ('wrong_answer','bad_distractors','unclear','off_topic','other')),
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quiz_quality_reports_question ON quiz_quality_reports (question_id);
`

// setupQuiz installs the Spec D quiz schema. Idempotent across cold boots
// (DROPs are guarded by IF EXISTS; CREATEs run once per fresh DB).
func setupQuiz(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, quizSchema); err != nil {
		return fmt.Errorf("exec quiz schema:\n%w", err)
	}
	return nil
}
