package quiz

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/aipipeline"
)

// AIDriver is the slice of the AI pipeline this package depends on.
// Defined as an interface so tests can swap in a fake.
type AIDriver interface {
	// GenerateQuiz produces a streaming AI run for FeatureGenerateQuiz.
	GenerateQuiz(ctx context.Context, in aipipeline.QuizGenerateInput) (*aipipeline.QuizGenerateOutput, error)
}

// Service is the domain-level quiz facade.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
	ai AIDriver      // ai produces quiz questions; may be nil for tests that only exercise non-generation methods
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, ai AIDriver) *Service {
	return &Service{db: db, ai: ai}
}
