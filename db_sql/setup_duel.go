package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const duelSchema = `
CREATE TABLE IF NOT EXISTS duels (
    id                BIGSERIAL PRIMARY KEY,
    challenger_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenger_name   TEXT NOT NULL,
    invitee_id        BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    invitee_name      TEXT NULL,
    subject_id        BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    subject_name      TEXT NOT NULL,
    status            TEXT NOT NULL,
    challenger_score  INT NOT NULL DEFAULT 0,
    invitee_score     INT NOT NULL DEFAULT 0,
    current_round     INT NOT NULL DEFAULT 0,
    rounds_total      INT NOT NULL DEFAULT 5,
    quiz_id           BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ NULL,
    finished_at       TIMESTAMPTZ NULL,
    CONSTRAINT duels_status_chk CHECK (status IN ('waiting','accepted','active','finished','canceled'))
);
CREATE INDEX IF NOT EXISTS idx_duels_challenger ON duels(challenger_id);
CREATE INDEX IF NOT EXISTS idx_duels_invitee ON duels(invitee_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_duels_one_waiting_per_challenger
  ON duels(challenger_id) WHERE status = 'waiting';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'quizzes_duel_id_fkey') THEN
    ALTER TABLE quizzes
      ADD CONSTRAINT quizzes_duel_id_fkey
      FOREIGN KEY (duel_id) REFERENCES duels(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS duel_invite_tokens (
    id          BIGSERIAL PRIMARY KEY,
    duel_id     BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS duel_round_questions (
    duel_id     BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no    INT NOT NULL,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    PRIMARY KEY (duel_id, round_no)
);

CREATE TABLE IF NOT EXISTS duel_round_answers (
    duel_id             BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no            INT NOT NULL,
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chosen_index        SMALLINT NOT NULL,
    is_correct          BOOLEAN NOT NULL,
    server_received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
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
    id           BIGSERIAL PRIMARY KEY,
    reporter_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason       TEXT NOT NULL,
    context      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupDuel(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, duelSchema); err != nil {
		return fmt.Errorf("exec duel schema:\n%w", err)
	}
	return nil
}
