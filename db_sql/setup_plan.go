package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TEMPORARY: destructive realignment for Spec B pre-launch (Plan B Task 1 —
// see docs/superpowers/plans/2026-04-30-ai-revision-plan.md). Replace with
// CREATE TABLE IF NOT EXISTS once all consumers reference the new column shape.
const planSchema = `
DROP TABLE IF EXISTS revision_plan_progress CASCADE;
DROP TABLE IF EXISTS revision_plans CASCADE;
DROP TABLE IF EXISTS exams CASCADE;

CREATE TABLE exams (
    id                BIGSERIAL    PRIMARY KEY,
    user_id           BIGINT       NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    subject_id        BIGINT       NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    date              DATE         NOT NULL,
    title             TEXT         NOT NULL,
    notes             TEXT,
    annales_image_id  TEXT         REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_exams_user_active ON exams (user_id, date);

CREATE TABLE revision_plans (
    id            BIGSERIAL    PRIMARY KEY,
    exam_id       BIGINT       NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    generated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    days          JSONB        NOT NULL,
    model         TEXT         NOT NULL,
    prompt_hash   TEXT         NOT NULL
);

CREATE TABLE revision_plan_progress (
    user_id   BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    fc_id     BIGINT      NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    plan_date DATE        NOT NULL,
    done_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, fc_id, plan_date)
);
CREATE INDEX idx_rpp_user_today ON revision_plan_progress (user_id, plan_date);
`

func setupPlan(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, planSchema); err != nil {
		return fmt.Errorf("exec plan schema:\n%w", err)
	}
	return nil
}
