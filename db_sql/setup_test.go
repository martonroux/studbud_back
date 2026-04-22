package db_sql

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSetupAllIsIdempotent(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || !strings.HasSuffix(dsn, "/studbud_test") {
		t.Skip("DATABASE_URL must point at studbud_test (set ENV=test)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if err := SetupAll(ctx, pool); err != nil {
		t.Fatalf("first SetupAll: %v", err)
	}
	if err := SetupAll(ctx, pool); err != nil {
		t.Fatalf("second SetupAll: %v", err)
	}
}
