package db_sql

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// planTestPool returns a process-shared pool with SetupAll already run.
// Lives in this file (not testutil) because db_sql tests cannot import
// testutil — testutil imports db_sql, which would cycle.
var (
	planPoolOnce sync.Once
	planPool     *pgxpool.Pool
	planPoolErr  error
)

// openPlanTestDB opens (once) the studbud_test pool and bootstraps schema.
func openPlanTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("ENV") != "test" {
		t.Skip("ENV must be 'test' to run DB-backed tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if !strings.HasSuffix(dsn, "/studbud_test") &&
		!strings.HasSuffix(dsn, "/studbud_test?sslmode=disable") {
		t.Fatalf("refusing to run tests against %q — must end with /studbud_test", dsn)
	}
	planPoolOnce.Do(func() {
		ctx := context.Background()
		planPool, planPoolErr = pgxpool.New(ctx, dsn)
		if planPoolErr != nil {
			return
		}
		planPoolErr = SetupAll(ctx, planPool)
	})
	if planPoolErr != nil {
		t.Fatalf("test db setup: %v", planPoolErr)
	}
	return planPool
}

// resetPlanTables truncates only the tables this test file touches.
func resetPlanTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        TRUNCATE TABLE
          revision_plan_progress, revision_plans, exams,
          flashcards, chapters, subjects, users
        RESTART IDENTITY CASCADE
    `)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// TestSetupPlan_PreservesExistingRows guards the idempotency contract: running
// setupPlan twice must not destroy data written between the two calls.
func TestSetupPlan_PreservesExistingRows(t *testing.T) {
	pool := openPlanTestDB(t)
	resetPlanTables(t, pool)
	ctx := context.Background()

	var userID, subjectID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash)
        VALUES ('plan-survive', 'plan-survive@example.com', 'x') RETURNING id
    `).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
        INSERT INTO subjects (owner_id, name) VALUES ($1, 'Bio') RETURNING id
    `, userID).Scan(&subjectID); err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	var examID int64
	if err := pool.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, date, title)
        VALUES ($1, $2, $3, 'Partiel') RETURNING id
    `, userID, subjectID, time.Now().AddDate(0, 0, 14)).Scan(&examID); err != nil {
		t.Fatalf("seed exam: %v", err)
	}

	if err := setupPlan(ctx, pool); err != nil {
		t.Fatalf("re-run setupPlan: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exams WHERE id = $1`, examID).Scan(&n); err != nil {
		t.Fatalf("count exams: %v", err)
	}
	if n != 1 {
		t.Fatalf("exam survived re-setup: count = %d, want 1", n)
	}
}

// TestSetupPlan_RevisionPlansHasGenerationID asserts the generation_id column
// is present on revision_plans (Spec B §4.2).
func TestSetupPlan_RevisionPlansHasGenerationID(t *testing.T) {
	pool := openPlanTestDB(t)
	ctx := context.Background()

	var n int
	err := pool.QueryRow(ctx, `
        SELECT count(*) FROM information_schema.columns
        WHERE table_name = 'revision_plans' AND column_name = 'generation_id'
    `).Scan(&n)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if n != 1 {
		t.Fatalf("revision_plans.generation_id missing: count = %d", n)
	}
}
