package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
)

// RunStructuredGeneration validates entitlement + quota + concurrency, inserts an
// ai_jobs row, then spawns a goroutine that drives the provider stream and emits
// validated AIChunks on the returned channel. The channel closes when the stream
// ends. Callers always receive jobID (for audit), even on synchronous errors.
func (s *Service) RunStructuredGeneration(
	ctx context.Context,
	req AIRequest,
) (<-chan AIChunk, int64, error) {
	if err := s.preflight(ctx, req); err != nil {
		return nil, 0, err
	}
	jobID, err := s.insertJob(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	out := make(chan AIChunk, 16)
	go s.drive(ctx, req, jobID, out)
	return out, jobID, nil
}

// preflight runs entitlement + quota + concurrent-cap checks in order.
func (s *Service) preflight(ctx context.Context, req AIRequest) error {
	if err := s.checkEntitlement(ctx, req.UserID); err != nil {
		return err
	}
	if err := s.CheckQuota(ctx, req.UserID, req.Feature, req.PDFPages); err != nil {
		return err
	}
	return s.checkConcurrency(ctx, req)
}

// checkEntitlement fails fast when the user lacks AI access.
func (s *Service) checkEntitlement(ctx context.Context, uid int64) error {
	ok, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return fmt.Errorf("has ai access:\n%w", err)
	}
	if !ok {
		return myErrors.ErrNoAIAccess
	}
	return nil
}

// checkConcurrency rejects a second generate request while one is already running.
// Check-flashcard calls are not capped this way.
func (s *Service) checkConcurrency(ctx context.Context, req AIRequest) error {
	if req.Feature == FeatureCheckFlashcard {
		return nil
	}
	var existingID int64
	err := s.db.QueryRow(ctx, sqlSelectRunningGenerationID, req.UserID).Scan(&existingID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("check concurrency:\n%w", err)
	}
	return &myErrors.AppError{
		Code:    "concurrent_generation",
		Message: fmt.Sprintf("generation already running (jobId #%d)", existingID),
		Wrapped: myErrors.ErrConflict,
	}
}

// insertJob creates the ai_jobs row and returns its id.
func (s *Service) insertJob(ctx context.Context, req AIRequest) (int64, error) {
	meta, err := json.Marshal(req.Metadata)
	if err != nil {
		meta = []byte(`{}`)
	}
	var subjectID, flashcardID *int64
	if req.SubjectID > 0 {
		subjectID = &req.SubjectID
	}
	if req.FlashcardID > 0 {
		flashcardID = &req.FlashcardID
	}
	var jobID int64
	err = s.db.QueryRow(ctx, sqlInsertAIJob,
		req.UserID, string(req.Feature), s.model,
		subjectID, flashcardID, req.PDFPages, meta,
	).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("insert ai_job:\n%w", err)
	}
	return jobID, nil
}

// drive is the placeholder stream driver filled in by Task 9.
// Task 8 ships a minimal version that closes immediately so tests that drain
// the channel don't deadlock.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	_ = s.finalizeSuccess(context.Background(), jobID, 0, 0, 0, 0, 0)
	out <- AIChunk{Kind: ChunkDone}
}

// finalizeSuccess marks a job complete with the provided telemetry.
func (s *Service) finalizeSuccess(ctx context.Context, jobID int64, inTok, outTok, cents, emitted, dropped int) error {
	_, err := s.db.Exec(ctx, sqlFinalizeAIJobSuccess, jobID, inTok, outTok, cents, emitted, dropped)
	if err != nil {
		return fmt.Errorf("finalize success:\n%w", err)
	}
	return nil
}
