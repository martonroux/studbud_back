package testutil

import (
	"context"

	"studbud/backend/internal/aiProvider"
)

// FakeAIClient replays a fixed sequence of chunks.
type FakeAIClient struct {
	Chunks []aiProvider.Chunk
	Err    error
}

// Stream returns a closed channel with the configured chunks.
func (f *FakeAIClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make(chan aiProvider.Chunk, len(f.Chunks))
	for _, c := range f.Chunks {
		out <- c
	}
	close(out)
	return out, nil
}
