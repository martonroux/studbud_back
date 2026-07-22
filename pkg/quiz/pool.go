package quiz

import (
	"context"
	"fmt"

	"studbud/backend/pkg/aipipeline"
)

// resolveCardPool materialises the flashcard pool for the generation request.
// Returns (typedCards, ids); both empty for KindGlobal.
func (s *Service) resolveCardPool(ctx context.Context, req GenerateRequest) ([]aipipeline.QuizSourceCard, []int64, error) {
	if req.Kind == KindGlobal {
		return nil, nil, nil
	}
	rows, err := s.db.Query(ctx, poolQuery(req), poolArgs(req)...)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve pool:\n%w", err)
	}
	defer rows.Close()

	var cards []aipipeline.QuizSourceCard
	var ids []int64
	for rows.Next() {
		var c aipipeline.QuizSourceCard
		if err := rows.Scan(&c.ID, &c.Title, &c.Question, &c.Answer); err != nil {
			return nil, nil, fmt.Errorf("scan card:\n%w", err)
		}
		cards = append(cards, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate cards:\n%w", err)
	}
	return cards, ids, nil
}

// badOkPredicate and duePredicate are the canonical SQL fragments for the
// bad_ok / due card filters. Shared between poolQuery (single-filter row
// selection) and the card-counts aggregation query (counts every filter at
// once) so the semantics never drift between the two call sites.
// last_result encoding: -1=new, 0=bad, 1=ok, 2=good.
// due_at is nullable for new cards, so the due predicate requires IS NOT NULL.
const (
	badOkPredicate = `f.last_result IN (0, 1)`
	duePredicate   = `f.due_at IS NOT NULL AND f.due_at <= now()`
)

// poolQuery returns the SELECT for the requested filter. Subject scope is enforced
// via flashcards.subject_id directly (no join needed); chapter scope optional.
func poolQuery(req GenerateRequest) string {
	q := `
SELECT f.id, f.title, f.question, f.answer
  FROM flashcards f
 WHERE f.subject_id = $1`
	if req.ChapterID != nil {
		q += ` AND f.chapter_id = $2`
	}
	switch req.CardFilter {
	case FilterBadOK:
		q += ` AND ` + badOkPredicate
	case FilterDue:
		q += ` AND ` + duePredicate
	}
	q += ` ORDER BY f.id`
	return q
}

// poolArgs builds the args slice matching poolQuery's placeholders.
func poolArgs(req GenerateRequest) []any {
	args := []any{req.SubjectID}
	if req.ChapterID != nil {
		args = append(args, *req.ChapterID)
	}
	return args
}

// ResolvePoolForTest exposes resolveCardPool for tests.
// Production callers must use Generate.
func (s *Service) ResolvePoolForTest(ctx context.Context, req GenerateRequest) ([]aipipeline.QuizSourceCard, []int64, error) {
	return s.resolveCardPool(ctx, req)
}
