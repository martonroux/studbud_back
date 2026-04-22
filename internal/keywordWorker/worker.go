package keywordWorker

import (
	"context"
	"log"
)

// Worker polls ai_extraction_jobs and drives keyword extraction.
// Real implementation arrives with Spec B.0.
type Worker struct{}

// New returns a no-op worker.
func New() *Worker { return &Worker{} }

// Start is a no-op until Spec B.0 lands.
func (w *Worker) Start(ctx context.Context) {
	log.Printf("keywordWorker: stub (disabled until Spec B.0)")
}
