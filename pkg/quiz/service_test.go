package quiz_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/quiz"
)

// TestNewService_AllowsNilAIDriver verifies the constructor accepts a nil
// AI driver — useful for tests that only exercise non-generation methods.
func TestNewService_AllowsNilAIDriver(t *testing.T) {
	var pool *pgxpool.Pool // unused for this smoke test
	svc := quiz.NewService(pool, nil)
	if svc == nil {
		t.Fatal("nil service")
	}
}
