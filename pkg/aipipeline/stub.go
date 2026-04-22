package aipipeline

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
)

// Service is the AI pipeline facade. In the skeleton it returns ErrNotImplemented;
// Spec A will replace the implementation (entitlement + quota + structured gen).
type Service struct {
	db  *pgxpool.Pool     // db is the shared pool (unused in the stub but retained for signature stability)
	cli aiProvider.Client // cli is the underlying AI provider (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool, cli aiProvider.Client) *Service {
	return &Service{db: db, cli: cli}
}

// GenerateFlashcards is a placeholder for prompt-based flashcard generation.
func (s *Service) GenerateFlashcards(ctx context.Context, uid, subjectID int64, prompt string) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}

// GenerateFromPDF is a placeholder for PDF-driven flashcard generation.
func (s *Service) GenerateFromPDF(ctx context.Context, uid, subjectID int64, pdfBytes []byte) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}

// CheckFlashcards is a placeholder for the "AI check" review pass.
func (s *Service) CheckFlashcards(ctx context.Context, uid, subjectID int64, cardIDs []int64) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}
