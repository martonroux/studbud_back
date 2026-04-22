package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const billingSchema = `
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id                        BIGSERIAL PRIMARY KEY,
    user_id                   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_subscription_id    TEXT UNIQUE NULL,
    plan                      TEXT NOT NULL,
    status                    TEXT NOT NULL,
    current_period_end        TIMESTAMPTZ NULL,
    cancel_at_period_end      BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_subs_plan_chk CHECK (plan IN ('pro_monthly','pro_annual','comp')),
    CONSTRAINT user_subs_status_chk CHECK (status IN ('active','past_due','canceled','trialing','comp'))
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
`

func setupBilling(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, billingSchema); err != nil {
		return fmt.Errorf("exec billing schema:\n%w", err)
	}
	return nil
}
