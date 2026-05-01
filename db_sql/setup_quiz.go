package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const quizSchema = `
CREATE TABLE IF NOT EXISTS quizzes (
    id             BIGSERIAL PRIMARY KEY,
    owner_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id     BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    title          TEXT NOT NULL,
    kind           TEXT NOT NULL,
    source         TEXT NOT NULL,
    parent_quiz_id BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    plan_id        BIGINT NULL REFERENCES revision_plans(id) ON DELETE SET NULL,
    duel_id        BIGINT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quizzes_source_check CHECK (source IN ('user','plan','shared_copy','duel')),
    CONSTRAINT quizzes_kind_check   CHECK (kind IN ('specific','global'))
);
CREATE INDEX IF NOT EXISTS idx_quizzes_owner ON quizzes(owner_id);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'quizzes_plan_id_fkey') THEN
    ALTER TABLE quizzes
      ADD CONSTRAINT quizzes_plan_id_fkey
      FOREIGN KEY (plan_id) REFERENCES revision_plans(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS quiz_questions (
    id                  BIGSERIAL PRIMARY KEY,
    quiz_id             BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    position            INT NOT NULL,
    prompt              TEXT NOT NULL,
    choices             JSONB NOT NULL,
    correct_index       SMALLINT NOT NULL,
    source_flashcard_id BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL,
    explanation         TEXT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
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
    quiz_id      BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);

CREATE TABLE IF NOT EXISTS quiz_quality_reports (
    id          BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupQuiz(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, quizSchema); err != nil {
		return fmt.Errorf("exec quiz schema:\n%w", err)
	}
	return nil
}
