package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const aiSchema = `
CREATE TABLE IF NOT EXISTS ai_jobs (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature_key   TEXT NOT NULL,
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cents_spent   INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL,
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
    status        TEXT NOT NULL DEFAULT 'pending',
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT NULL,
    claimed_at    TIMESTAMPTZ NULL,
    finished_at   TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flashcard_id),
    CONSTRAINT ai_extraction_jobs_status_chk CHECK (status IN ('pending','claimed','succeeded','failed'))
);
CREATE INDEX IF NOT EXISTS idx_ai_extraction_jobs_status ON ai_extraction_jobs(status);

CREATE TABLE IF NOT EXISTS flashcard_keywords (
    flashcard_id BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword      TEXT NOT NULL,
    weight       REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (flashcard_id, keyword)
);
CREATE INDEX IF NOT EXISTS idx_flashcard_keywords_kw ON flashcard_keywords(keyword);
`

func setupAI(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, aiSchema); err != nil {
		return fmt.Errorf("exec ai schema:\n%w", err)
	}
	return nil
}
