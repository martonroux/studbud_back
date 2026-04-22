package plan

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Service is the revision-plan facade. Spec B will replace this.
type Service struct {
	db *pgxpool.Pool // db is the shared pool (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Generate is a placeholder for building a new revision plan for an exam.
func (s *Service) Generate(ctx context.Context, uid, examID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Progress is a placeholder for plan progress state.
func (s *Service) Progress(ctx context.Context, uid, planID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}
