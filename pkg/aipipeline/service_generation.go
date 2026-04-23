package aipipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"studbud/backend/internal/aiProvider"
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

// drive runs the provider stream, parses incoming JSON into array elements,
// emits ChunkItem / ChunkDone / ChunkError, and finalizes the ai_jobs row.
func (s *Service) drive(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) {
	defer close(out)
	result := s.streamOnce(ctx, req, jobID, out)
	s.finalize(ctx, jobID, req, result, out)
}

// streamResult aggregates what happened during one provider stream.
type streamResult struct {
	inputTokens  int   // inputTokens is the provider's prompt-token count
	outputTokens int   // outputTokens is the provider's completion-token count
	centsSpent   int   // centsSpent is the rounded cost estimate
	emitted      int   // emitted counts items that passed validation
	dropped      int   // dropped counts items that failed validation
	err          error // err is nil on success
}

// streamOnce calls the provider once and drives the parser. Caller handles retry.
func (s *Service) streamOnce(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) streamResult {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		PDFBytes:   req.PDFBytes,
	})
	if err != nil {
		return streamResult{err: classifyProviderStartErr(err)}
	}
	return s.consumeStream(ctx, chunks, out)
}

// consumeStream reads chunks, forwards items, counts accepted/dropped.
func (s *Service) consumeStream(ctx context.Context, chunks <-chan aiProvider.Chunk, out chan<- AIChunk) streamResult {
	r := streamResult{}
	p := newArrayParser("items")
	p.onElement = func(b []byte) {
		if isWellFormedObject(b) {
			cp := append([]byte(nil), b...)
			select {
			case out <- AIChunk{Kind: ChunkItem, Item: cp}:
				r.emitted++
			case <-ctx.Done():
			}
		} else {
			r.dropped++
		}
	}
	for {
		select {
		case <-ctx.Done():
			r.err = ctx.Err()
			return r
		case c, ok := <-chunks:
			if !ok {
				return r
			}
			p.feed([]byte(c.Text))
			if c.Done {
				return r
			}
		}
	}
}

// finalize writes the terminal state to ai_jobs and emits the last chunk.
func (s *Service) finalize(ctx context.Context, jobID int64, req AIRequest, r streamResult, out chan<- AIChunk) {
	bg := context.Background() // decouple finalize from request cancellation
	if r.err != nil {
		s.finalizeError(bg, jobID, r, out)
		return
	}
	_ = s.finalizeSuccess(bg, jobID, r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped)
	if r.emitted > 0 {
		_ = s.DebitQuota(bg, req.UserID, req.Feature, 1, 0)
	}
	out <- AIChunk{Kind: ChunkDone}
}

// finalizeError marks the job failed and surfaces the error to the client.
func (s *Service) finalizeError(ctx context.Context, jobID int64, r streamResult, out chan<- AIChunk) {
	kind, msg := classifyErrForPersistence(r.err)
	_, _ = s.db.Exec(ctx, sqlFinalizeAIJobFailure, jobID, statusFor(r.err),
		r.inputTokens, r.outputTokens, r.centsSpent, r.emitted, r.dropped, kind, msg)
	out <- AIChunk{Kind: ChunkError, Err: r.err}
}

// finalizeSuccess marks a job complete with the provided telemetry.
func (s *Service) finalizeSuccess(ctx context.Context, jobID int64, inTok, outTok, cents, emitted, dropped int) error {
	_, err := s.db.Exec(ctx, sqlFinalizeAIJobSuccess, jobID, inTok, outTok, cents, emitted, dropped)
	if err != nil {
		return fmt.Errorf("finalize success:\n%w", err)
	}
	return nil
}
