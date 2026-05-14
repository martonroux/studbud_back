package aipipeline

import (
	"context"

	"studbud/backend/internal/aiProvider"
)

// QuizGenerateInput is the typed entry point for FeatureGenerateQuiz.
type QuizGenerateInput struct {
	UserID    int64                  // UserID owns the generation
	SubjectID int64                  // SubjectID anchors the prompt + quota check
	Prompt    string                 // Prompt is the rendered template body (see RenderGenerateQuiz)
	Metadata  map[string]any         // Metadata is forwarded into ai_jobs.metadata for debugging
	Images    []aiProvider.ImagePart // Images is always empty for quiz (kept for symmetry with other features)
}

// QuizGenerateOutput is the typed stream + job handle returned to callers.
type QuizGenerateOutput struct {
	Chunks <-chan AIChunk // Chunks is the streaming validation channel
	JobID  int64          // JobID is the ai_jobs row this run is associated with
}

// GenerateQuiz wraps RunStructuredGeneration with the FeatureGenerateQuiz feature key.
// Quota is debited only on success (handled by the underlying primitive's
// post-run accounting).
func (s *Service) GenerateQuiz(ctx context.Context, in QuizGenerateInput) (*QuizGenerateOutput, error) {
	req := AIRequest{
		UserID:    in.UserID,
		Feature:   FeatureGenerateQuiz,
		SubjectID: in.SubjectID,
		Prompt:    in.Prompt,
		Images:    in.Images,
		Metadata:  in.Metadata,
	}
	ch, jobID, err := s.RunStructuredGeneration(ctx, req)
	if err != nil {
		return nil, err
	}
	return &QuizGenerateOutput{Chunks: ch, JobID: jobID}, nil
}
