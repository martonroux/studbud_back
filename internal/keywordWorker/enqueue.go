package keywordWorker

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Priority is an enqueue ordering hint. Higher = picked first.
type Priority int16

const (
	// PriorityBackfill is reserved for migration-time bulk enqueue.
	PriorityBackfill Priority = -1
	// PriorityRetry is used when re-enqueueing after a transient failure.
	PriorityRetry Priority = 0
	// PriorityUser is the default for create/update-triggered jobs.
	PriorityUser Priority = 1
)

// Enqueuer inserts (and dedups) keyword-extraction jobs.
// Implements pkg/flashcard.KeywordEnqueuer.
type Enqueuer struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewEnqueuer constructs an Enqueuer.
func NewEnqueuer(db *pgxpool.Pool) *Enqueuer {
	return &Enqueuer{db: db}
}

// EnqueueForFlashcard inserts a pending job for fcID, or bumps the priority of
// an existing pending/running row to max(existing, prio).
func (e *Enqueuer) EnqueueForFlashcard(ctx context.Context, fcID int64, prio Priority) error {
	if _, err := e.db.Exec(ctx, sqlEnqueueJob, fcID, int16(prio)); err != nil {
		return fmt.Errorf("enqueue extraction job %d:\n%w", fcID, err)
	}

	return nil
}

// MaterialChange returns true when the (oldQ,oldA) → (newQ,newA) edit is large enough
// that re-extracting keywords is worth the AI call.
//
// Both conditions must hold: absolute byte-length delta of the joined Q+A is
// >= 20 chars, AND levenshteinRatio(old, new) >= 0.10. This avoids re-extraction
// on typo fixes and trailing-whitespace edits while still catching meaningful
// additions or rewrites.
func MaterialChange(oldQ, oldA, newQ, newA string) bool {
	const sep = "\x00"

	oldCombined := oldQ + sep + oldA
	newCombined := newQ + sep + newA

	if oldCombined == newCombined {
		return false
	}

	lenDelta := abs(len(newCombined) - len(oldCombined))

	if lenDelta < 20 {
		return false
	}

	return levenshteinRatio(oldCombined, newCombined) >= 0.10
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}

	return x
}

// levenshteinRatio returns the bounded edit distance divided by the longer
// string's length. Distances above ceil(0.10 * maxLen) are reported as the
// cap+1 value, which still yields a ratio >= 0.10 — sufficient for the caller.
func levenshteinRatio(a, b string) float64 {
	maxLen := math.Max(float64(len(a)), float64(len(b)))

	if maxLen == 0 {
		return 0
	}

	cap := int(math.Ceil(maxLen * 0.10))

	if cap < 1 {
		cap = 1
	}

	dist := boundedLevenshtein(a, b, cap)

	return float64(dist) / maxLen
}

// boundedLevenshtein computes the edit distance up to bound. If the true
// distance exceeds bound, returns bound+1 (signal that the change is at least
// bound+1).
func boundedLevenshtein(a, b string, bound int) int {
	la, lb := len(a), len(b)

	if abs(la-lb) > bound {
		return bound + 1
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		minRow := fillLevRow(a, b, i, prev, curr)

		if minRow > bound {
			return bound + 1
		}

		prev, curr = curr, prev
	}

	return prev[lb]
}

// fillLevRow fills curr[0..lb] for row i of the Levenshtein matrix and returns
// the minimum value in the row (used for the early-exit bound check).
func fillLevRow(a, b string, i int, prev, curr []int) int {
	curr[0] = i
	minRow := curr[0]

	for j := 1; j <= len(b); j++ {
		cost := 1

		if a[i-1] == b[j-1] {
			cost = 0
		}

		curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)

		if curr[j] < minRow {
			minRow = curr[j]
		}
	}

	return minRow
}

// min3 returns the smallest of a, b, c.
func min3(a, b, c int) int {
	m := a

	if b < m {
		m = b
	}

	if c < m {
		m = c
	}

	return m
}
