package plan

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// shortlistSQL finds flashcards in OTHER accessible subjects that share at least
// `minOverlap` keywords with any flashcard in the exam's primary subject.
//
// Accessibility is computed inline as: subject is owned, subject visibility is
// 'public', the user is a collaborator, or visibility is 'friends' and the user
// is friends with the owner.
const shortlistSQL = `
WITH primary_keywords AS (
    SELECT DISTINCT fk.keyword
    FROM flashcards fc
    JOIN flashcard_keywords fk ON fk.flashcard_id = fc.id
    WHERE fc.subject_id = $2
),
accessible_subjects AS (
    SELECT id FROM subjects WHERE owner_id = $1
    UNION
    SELECT subject_id FROM collaborators WHERE user_id = $1
    UNION
    SELECT id FROM subjects WHERE visibility = 'public'
    UNION
    SELECT s.id FROM subjects s
        JOIN friendships f ON f.status = 'accepted'
            AND ((f.sender_id = $1 AND f.receiver_id = s.owner_id)
              OR (f.receiver_id = $1 AND f.sender_id = s.owner_id))
        WHERE s.visibility = 'friends'
),
candidate_fcs AS (
    SELECT fk.flashcard_id AS fc_id, COUNT(*) AS overlap_score, SUM(fk.weight) AS weight_sum
    FROM flashcard_keywords fk
    JOIN primary_keywords pk ON pk.keyword = fk.keyword
    JOIN flashcards fc ON fc.id = fk.flashcard_id
    WHERE fc.subject_id <> $2
      AND fc.subject_id IN (SELECT id FROM accessible_subjects)
    GROUP BY fk.flashcard_id
    HAVING COUNT(*) >= $3
)
SELECT fc.id, fc.title, fc.subject_id, s.name, c.overlap_score, c.weight_sum
FROM candidate_fcs c
JOIN flashcards fc ON fc.id = c.fc_id
JOIN subjects s ON s.id = fc.subject_id
ORDER BY c.weight_sum DESC, c.overlap_score DESC, fc.id ASC
LIMIT $4
`

// minKeywordOverlap is the minimum number of shared keywords required for a candidate.
// Keyword=1 alone is too noisy; >=2 is the spec-locked sweet spot.
const minKeywordOverlap = 2

// Shortlist returns up to `limit` cross-subject flashcards sharing keywords with
// any flashcard in the exam's primary subject. Excludes the primary subject itself
// and any subject the user cannot read.
func Shortlist(ctx context.Context, db *pgxpool.Pool, userID, examSubjectID int64, limit int) ([]Candidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, shortlistSQL, userID, examSubjectID, minKeywordOverlap, limit)
	if err != nil {
		return nil, fmt.Errorf("shortlist query:\n%w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ID, &c.Title, &c.SubjectID, &c.SubjectName, &c.OverlapScore, &c.WeightSum); err != nil {
			return nil, fmt.Errorf("scan candidate:\n%w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates:\n%w", err)
	}
	return attachKeywords(ctx, db, out)
}

// attachKeywords loads keyword lists for each candidate so the AI prompt can
// surface the actual overlapping keywords. Empty slice in, empty slice out.
func attachKeywords(ctx context.Context, db *pgxpool.Pool, cs []Candidate) ([]Candidate, error) {
	if len(cs) == 0 {
		return cs, nil
	}
	ids := make([]int64, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	kwByID, err := loadKeywords(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		cs[i].Keywords = kwByID[cs[i].ID]
	}
	return cs, nil
}

// loadKeywords fetches the keyword list for each flashcard ID in one query.
func loadKeywords(ctx context.Context, db *pgxpool.Pool, ids []int64) (map[int64][]string, error) {
	rows, err := db.Query(ctx, `
        SELECT flashcard_id, keyword FROM flashcard_keywords
        WHERE flashcard_id = ANY($1)
    `, ids)
	if err != nil {
		return nil, fmt.Errorf("load keywords:\n%w", err)
	}
	defer rows.Close()
	out := make(map[int64][]string, len(ids))
	for rows.Next() {
		var fcID int64
		var kw string
		if err := rows.Scan(&fcID, &kw); err != nil {
			return nil, fmt.Errorf("scan keyword:\n%w", err)
		}
		out[fcID] = append(out[fcID], kw)
	}
	return out, rows.Err()
}
