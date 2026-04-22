package aiProvider

import (
	"context"

	"studbud/backend/internal/myErrors"
)

// Chunk is one streamed piece of AI output.
type Chunk struct {
	Text string
	Done bool
}

// Request is the structured-generation invocation shape.
type Request struct {
	FeatureKey string
	Model      string
	Prompt     string
	PDFBytes   []byte
}

// Client is the AI provider interface. Real Anthropic impl arrives with Spec A.
type Client interface {
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

// Stream always returns ErrNotImplemented.
func (NoopClient) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return nil, myErrors.ErrNotImplemented
}
