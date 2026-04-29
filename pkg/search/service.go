package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service exposes read-only search queries.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// SubjectHit is a trimmed subject row returned by search.
type SubjectHit struct {
	ID         int64  `json:"id"`         // ID is the subject id
	OwnerID    int64  `json:"owner_id"`   // OwnerID is the creator
	Name       string `json:"name"`       // Name is the subject name
	Visibility string `json:"visibility"` // Visibility is private|friends|public
}

// UserHit is a trimmed user row returned by search.
type UserHit struct {
	ID       int64  `json:"id"`       // ID is the user id
	Username string `json:"username"` // Username is the user's handle
}

// FlashcardHit is a trimmed flashcard row returned by search.
type FlashcardHit struct {
	ID        int64  `json:"id"`         // ID is the flashcard id
	SubjectID int64  `json:"subject_id"` // SubjectID is the owning subject
	ChapterID *int64 `json:"chapter_id"` // ChapterID is the optional chapter
	Title     string `json:"title"`      // Title is the flashcard heading
	Question  string `json:"question"`   // Question is the flashcard prompt
}

// normalizePage clamps limit to [1,50] (default 20) and offset to >=0.
func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// buildPrefixTSQuery turns a user query like "chem micro" into a to_tsquery-safe
// expression "chem:* & micro:*" that prefix-matches each token. Returns "" when
// the input contains no alphanumeric runs (callers should treat that as no-op).
func buildPrefixTSQuery(q string) string {
	var tokens []string
	for _, field := range strings.Fields(q) {
		var b strings.Builder
		for _, r := range field {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			tokens = append(tokens, b.String()+":*")
		}
	}
	return strings.Join(tokens, " & ")
}

// OwnedSubjects searches subjects owned by the caller using pg_trgm substring
// matching on name+tags. The owner_id filter keeps the scan tiny per user, so
// ILIKE does not need a dedicated trigram index on subjects.name.
// includeArchived = true returns archived subjects alongside live ones.
func (s *Service) OwnedSubjects(ctx context.Context, uid int64, query string, includeArchived bool, limit, offset int) ([]SubjectHit, error) {
	limit, offset = normalizePage(limit, offset)
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, visibility
		FROM subjects
		WHERE owner_id = $1
		  AND ($5::bool OR archived = false)
		  AND (name ILIKE '%' || $2 || '%' OR tags ILIKE '%' || $2 || '%')
		ORDER BY GREATEST(similarity(name, $2), similarity(tags, $2)) DESC, id DESC
		LIMIT $3 OFFSET $4
	`, uid, q, limit, offset, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("search owned subjects:\n%w", err)
	}
	defer rows.Close()
	return scanSubjectHits(rows)
}

// PublicSubjects searches public subjects authored by someone other than the
// caller using tsvector + prefix matching. Keeps the GIN index hot so scaling
// to a large community catalogue stays cheap.
func (s *Service) PublicSubjects(ctx context.Context, uid int64, query string, limit, offset int) ([]SubjectHit, error) {
	limit, offset = normalizePage(limit, offset)
	tsq := buildPrefixTSQuery(query)
	if tsq == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, visibility
		FROM subjects
		WHERE archived = false
		  AND visibility = 'public'
		  AND owner_id <> $1
		  AND search_vec @@ to_tsquery('simple', $2)
		ORDER BY ts_rank(search_vec, to_tsquery('simple', $2)) DESC, id DESC
		LIMIT $3 OFFSET $4
	`, uid, tsq, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search public subjects:\n%w", err)
	}
	defer rows.Close()
	return scanSubjectHits(rows)
}

// scanSubjectHits materializes a subject-hit rowset.
func scanSubjectHits(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]SubjectHit, error) {
	var out []SubjectHit
	for rows.Next() {
		var h SubjectHit
		if err := rows.Scan(&h.ID, &h.OwnerID, &h.Name, &h.Visibility); err != nil {
			return nil, fmt.Errorf("scan subject hit:\n%w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Users searches by username prefix / substring.
func (s *Service) Users(ctx context.Context, query string, limit, offset int) ([]UserHit, error) {
	limit, offset = normalizePage(limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT id, username FROM users
		WHERE username ILIKE '%' || $1 || '%'
		ORDER BY length(username) ASC, id ASC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search users:\n%w", err)
	}
	defer rows.Close()
	var out []UserHit
	for rows.Next() {
		var h UserHit
		if err := rows.Scan(&h.ID, &h.Username); err != nil {
			return nil, fmt.Errorf("scan user hit:\n%w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Flashcards searches cards the caller has explicitly brought into their
// library: owner, collaborator, subscribed-to public subjects, or friends-only
// subjects where the caller is an accepted friend of the owner. Random public
// content is intentionally NOT searched — public discovery happens at the
// subject level via /search/subjects/public.
// includeArchived = true unhides cards from the caller's OWN archived subjects;
// non-owned (collaborator / subscribed / friend) subjects are always filtered
// to archived = false regardless of the flag.
func (s *Service) Flashcards(ctx context.Context, uid int64, query string, includeArchived bool, limit, offset int) ([]FlashcardHit, error) {
	limit, offset = normalizePage(limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.subject_id, f.chapter_id, f.title, f.question
		FROM flashcards f
		JOIN subjects s ON s.id = f.subject_id
		WHERE (f.title ILIKE '%' || $2 || '%'
		       OR f.question ILIKE '%' || $2 || '%'
		       OR f.answer ILIKE '%' || $2 || '%')
		  AND (
		        (s.owner_id = $1 AND ($5::bool OR s.archived = false))
		     OR (s.archived = false AND (
		              EXISTS (SELECT 1 FROM collaborators c WHERE c.subject_id = s.id AND c.user_id = $1)
		           OR EXISTS (SELECT 1 FROM subject_subscriptions sub WHERE sub.subject_id = s.id AND sub.user_id = $1)
		           OR (s.visibility = 'friends' AND EXISTS (
		                 SELECT 1 FROM friendships fr
		                 WHERE fr.status = 'accepted'
		                   AND ((fr.sender_id = $1 AND fr.receiver_id = s.owner_id)
		                     OR (fr.sender_id = s.owner_id AND fr.receiver_id = $1))
		              ))
		        ))
		  )
		ORDER BY GREATEST(similarity(f.title, $2), similarity(f.question, $2), similarity(f.answer, $2)) DESC, f.id DESC
		LIMIT $3 OFFSET $4
	`, uid, query, limit, offset, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("search flashcards:\n%w", err)
	}
	defer rows.Close()
	var out []FlashcardHit
	for rows.Next() {
		var h FlashcardHit
		if err := rows.Scan(&h.ID, &h.SubjectID, &h.ChapterID, &h.Title, &h.Question); err != nil {
			return nil, fmt.Errorf("scan flashcard hit:\n%w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
