package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupAll runs every schema setup step in dependency order.
// All statements are idempotent: safe to run on every boot.
func SetupAll(ctx context.Context, pool *pgxpool.Pool) error {
	steps := []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool) error
	}{
		{"core", setupCore},
		{"ai", setupAI},
		{"billing", setupBilling},
		{"plan", setupPlan},
		{"quiz", setupQuiz},
		{"duel", setupDuel},
	}
	for _, s := range steps {
		if err := s.fn(ctx, pool); err != nil {
			return fmt.Errorf("setup %s:\n%w", s.name, err)
		}
	}
	return nil
}
