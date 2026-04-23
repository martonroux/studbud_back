package search

import (
	"context"
	"fmt"

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

// Subjects searches public subjects + the caller's own subjects by query.
func (s *Service) Subjects(ctx context.Context, uid int64, query string, limit int) ([]SubjectHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, visibility
		FROM subjects
		WHERE archived = false
		  AND (owner_id = $1 OR visibility = 'public')
		  AND search_vec @@ plainto_tsquery('simple', $2)
		ORDER BY ts_rank(search_vec, plainto_tsquery('simple', $2)) DESC, id DESC
		LIMIT $3
	`, uid, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search subjects:\n%w", err)
	}
	defer rows.Close()
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
func (s *Service) Users(ctx context.Context, query string, limit int) ([]UserHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, username FROM users
		WHERE username ILIKE '%' || $1 || '%'
		ORDER BY length(username) ASC, id ASC
		LIMIT $2
	`, query, limit)
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

// Flashcards searches cards the caller can at least view: owned subjects,
// subjects where the caller is a collaborator, public subjects, and
// friend-visible subjects where the caller is a friend of the owner.
func (s *Service) Flashcards(ctx context.Context, uid int64, query string, limit int) ([]FlashcardHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.subject_id, f.chapter_id, f.title, f.question
		FROM flashcards f
		JOIN subjects s ON s.id = f.subject_id
		WHERE s.archived = false
		  AND f.search_vec @@ plainto_tsquery('simple', $2)
		  AND (
		        s.owner_id = $1
		     OR s.visibility = 'public'
		     OR EXISTS (SELECT 1 FROM collaborators c WHERE c.subject_id = s.id AND c.user_id = $1)
		     OR (s.visibility = 'friends' AND EXISTS (
		           SELECT 1 FROM friendships fr
		           WHERE fr.status = 'accepted'
		             AND ((fr.sender_id = $1 AND fr.receiver_id = s.owner_id)
		               OR (fr.sender_id = s.owner_id AND fr.receiver_id = $1))
		        ))
		  )
		ORDER BY ts_rank(f.search_vec, plainto_tsquery('simple', $2)) DESC, f.id DESC
		LIMIT $3
	`, uid, query, limit)
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
