package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const planSchema = `
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
    id             BIGSERIAL PRIMARY KEY,
    exam_id        BIGINT NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    intensity      TEXT NOT NULL DEFAULT 'balanced',
    payload        JSONB NOT NULL,
    generated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT revision_plans_intensity_chk CHECK (intensity IN ('light','balanced','intense'))
);

CREATE TABLE IF NOT EXISTS revision_plan_progress (
    plan_id     BIGINT NOT NULL REFERENCES revision_plans(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    item_key    TEXT NOT NULL,
    done_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, day, item_key)
);
`

func setupPlan(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, planSchema); err != nil {
		return fmt.Errorf("exec plan schema:\n%w", err)
	}
	return nil
}
