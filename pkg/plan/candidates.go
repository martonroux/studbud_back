package plan

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// minPrimaryCardsForGeneration is the floor below which plan generation is refused.
const minPrimaryCardsForGeneration = 5

// maxPrimaryCardsInPrompt caps how many primary-subject FCs we surface to the AI.
const maxPrimaryCardsInPrompt = 200

// loadPrimaryCards returns the subject's flashcards (capped) along with their keywords.
// Cards without keywords still appear; their Keywords slice is empty.
func (s *Service) loadPrimaryCards(ctx context.Context, subjectID int64) ([]PrimaryCard, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, title FROM flashcards
        WHERE subject_id = $1
        ORDER BY id ASC
        LIMIT $2
    `, subjectID, maxPrimaryCardsInPrompt)
	if err != nil {
		return nil, fmt.Errorf("load primary cards:\n%w", err)
	}
	defer rows.Close()

	cards, err := scanPrimaryCards(rows)
	if err != nil {
		return nil, err
	}
	return s.attachPrimaryKeywords(ctx, cards)
}

// scanPrimaryCards reads each row into a PrimaryCard.
func scanPrimaryCards(rows pgx.Rows) ([]PrimaryCard, error) {
	var cards []PrimaryCard
	for rows.Next() {
		var c PrimaryCard
		if err := rows.Scan(&c.ID, &c.Title); err != nil {
			return nil, fmt.Errorf("scan primary card:\n%w", err)
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate primary cards:\n%w", err)
	}
	return cards, nil
}

// attachPrimaryKeywords loads the keyword list for each primary card in one query.
func (s *Service) attachPrimaryKeywords(ctx context.Context, cards []PrimaryCard) ([]PrimaryCard, error) {
	if len(cards) == 0 {
		return cards, nil
	}
	ids := make([]int64, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	kwByID, err := loadKeywords(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	for i := range cards {
		cards[i].Keywords = kwByID[cards[i].ID]
	}
	return cards, nil
}

// stateCounts captures the user's flashcard distribution across `last_result` buckets.
type stateCounts struct {
	New  int // New is cards never trained (last_result = -1)
	Bad  int // Bad is cards last marked bad (last_result = 0)
	OK   int // OK is cards last marked ok (last_result = 1)
	Good int // Good is cards last marked good (last_result = 2)
}

// loadStateCounts aggregates the user's card states for a subject for the prompt context.
// We scope to the exam's subject because cross-subject stats would dilute the signal.
func (s *Service) loadStateCounts(ctx context.Context, subjectID int64) (stateCounts, error) {
	var c stateCounts
	err := s.db.QueryRow(ctx, `
        SELECT
          count(*) FILTER (WHERE last_result = -1),
          count(*) FILTER (WHERE last_result = 0),
          count(*) FILTER (WHERE last_result = 1),
          count(*) FILTER (WHERE last_result = 2)
        FROM flashcards WHERE subject_id = $1
    `, subjectID).Scan(&c.New, &c.Bad, &c.OK, &c.Good)
	if err != nil {
		return c, fmt.Errorf("load state counts:\n%w", err)
	}
	return c, nil
}
