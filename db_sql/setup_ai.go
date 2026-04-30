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

DROP TABLE IF EXISTS flashcard_keywords;
DROP TABLE IF EXISTS ai_extraction_jobs;

CREATE TABLE ai_extraction_jobs (
    id           BIGSERIAL    PRIMARY KEY,
    fc_id        BIGINT       NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    priority     SMALLINT     NOT NULL DEFAULT 0,
    state        TEXT         NOT NULL CHECK (state IN ('pending','running','done','failed')),
    attempts     SMALLINT     NOT NULL DEFAULT 0,
    last_error   TEXT,
    enqueued_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX uniq_extraction_in_flight
    ON ai_extraction_jobs (fc_id)
    WHERE state IN ('pending','running');
CREATE INDEX idx_extraction_pickup
    ON ai_extraction_jobs (priority DESC, enqueued_at ASC)
    WHERE state = 'pending';

CREATE TABLE flashcard_keywords (
    fc_id   BIGINT  NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword TEXT    NOT NULL,
    weight  REAL    NOT NULL DEFAULT 1.0 CHECK (weight >= 0 AND weight <= 1),
    PRIMARY KEY (fc_id, keyword)
);
CREATE INDEX idx_flashcard_keywords_kw ON flashcard_keywords(keyword);

ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS subject_id      BIGINT NULL REFERENCES subjects(id)   ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS flashcard_id    BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS pdf_page_count  INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_emitted   INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS items_dropped   INT    NOT NULL DEFAULT 0;
ALTER TABLE ai_jobs ADD COLUMN IF NOT EXISTS error_kind      TEXT   NULL;
CREATE INDEX IF NOT EXISTS idx_ai_jobs_user_running ON ai_jobs(user_id) WHERE status = 'running';
`

func setupAI(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, aiSchema); err != nil {
		return fmt.Errorf("exec ai schema:\n%w", err)
	}
	return nil
}
