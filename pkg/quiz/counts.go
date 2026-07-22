package quiz

import (
	"context"
	"fmt"
)

// CardCounts is the {all, bad_ok, due} triple for a subject/chapter scope.
// Semantics match poolQuery exactly (badOkPredicate / duePredicate).
type CardCounts struct {
	All   int `json:"all"`
	BadOK int `json:"bad_ok"`
	Due   int `json:"due"`
}

// ChapterCardCounts pairs a chapter with its own CardCounts.
type ChapterCardCounts struct {
	ChapterID int64      `json:"chapterId"`
	Title     string     `json:"title"`
	Counts    CardCounts `json:"counts"`
}

// CardCountsRequest scopes a CardCounts lookup to a subject, optionally narrowed to a chapter.
type CardCountsRequest struct {
	UserID    int64  // UserID is the authenticated caller; enforces subject ownership
	SubjectID int64  // SubjectID anchors the lookup
	ChapterID *int64 // ChapterID narrows to one chapter; nil = whole subject
}

// CardCountsResult is the output of Service.CardCounts.
type CardCountsResult struct {
	Counts   CardCounts          // Counts is the total for the requested scope
	Chapters []ChapterCardCounts // Chapters is the per-chapter breakdown; nil when ChapterID was set
}

// countsQuery returns the SELECT that aggregates all/bad_ok/due in one pass over flashcards.
func countsQuery(chapterScoped bool) string {
	q := `
SELECT count(*),
       count(*) FILTER (WHERE ` + badOkPredicate + `),
       count(*) FILTER (WHERE ` + duePredicate + `)
  FROM flashcards f
 WHERE f.subject_id = $1`
	if chapterScoped {
		q += ` AND f.chapter_id = $2`
	}
	return q
}

// CardCounts computes the {all, bad_ok, due} counts for the requested scope.
// Enforces that req.UserID owns req.SubjectID (like other quiz endpoints).
// When req.ChapterID is nil, also returns the per-chapter breakdown for the subject.
func (s *Service) CardCounts(ctx context.Context, req CardCountsRequest) (CardCountsResult, error) {
	if _, err := s.lookupSubjectName(ctx, req.SubjectID, req.UserID); err != nil {
		return CardCountsResult{}, err
	}

	args := []any{req.SubjectID}
	if req.ChapterID != nil {
		args = append(args, *req.ChapterID)
	}
	var counts CardCounts
	err := s.db.QueryRow(ctx, countsQuery(req.ChapterID != nil), args...).
		Scan(&counts.All, &counts.BadOK, &counts.Due)
	if err != nil {
		return CardCountsResult{}, fmt.Errorf("card counts:\n%w", err)
	}

	if req.ChapterID != nil {
		return CardCountsResult{Counts: counts}, nil
	}

	chapters, err := s.chapterCardCounts(ctx, req.SubjectID)
	if err != nil {
		return CardCountsResult{}, err
	}
	return CardCountsResult{Counts: counts, Chapters: chapters}, nil
}

// chapterCardCounts returns the per-chapter {all, bad_ok, due} breakdown for a subject,
// including chapters with zero cards.
func (s *Service) chapterCardCounts(ctx context.Context, subjectID int64) ([]ChapterCardCounts, error) {
	rows, err := s.db.Query(ctx, `
SELECT c.id, c.title,
       count(f.id),
       count(f.id) FILTER (WHERE `+badOkPredicate+`),
       count(f.id) FILTER (WHERE `+duePredicate+`)
  FROM chapters c
  LEFT JOIN flashcards f ON f.chapter_id = c.id
 WHERE c.subject_id = $1
 GROUP BY c.id, c.title
 ORDER BY c.position, c.id`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("chapter card counts:\n%w", err)
	}
	defer rows.Close()

	out := []ChapterCardCounts{}
	for rows.Next() {
		var cc ChapterCardCounts
		if err := rows.Scan(&cc.ChapterID, &cc.Title, &cc.Counts.All, &cc.Counts.BadOK, &cc.Counts.Due); err != nil {
			return nil, fmt.Errorf("scan chapter card counts:\n%w", err)
		}
		out = append(out, cc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chapter card counts:\n%w", err)
	}
	return out, nil
}
