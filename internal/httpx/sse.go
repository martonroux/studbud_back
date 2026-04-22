package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Stream writes chunks from a channel to the response as SSE events.
// Each chunk is JSON-encoded as `data: {...}\n\n`. Closes when the channel
// closes or ctx is canceled. Safe to use from HTTP handlers.
func Stream(ctx context.Context, w http.ResponseWriter, chunks <-chan any) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return nil
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			b, err := json.Marshal(chunk)
			if err != nil {
				return fmt.Errorf("marshal chunk:\n%w", err)
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return fmt.Errorf("write chunk:\n%w", err)
			}
			flusher.Flush()
		}
	}
}
