package flashcard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns flashcard CRUD and lightweight review tracking.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access enforces subject-scoped permissions
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a flashcard.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Flashcard, error) {
	if in.Question == "" || in.Answer == "" {
		return nil, myErrors.ErrInvalidInput
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Source != "manual" && in.Source != "ai" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureEdit(ctx, uid, in.SubjectID); err != nil {
		return nil, err
	}
	fc, err := s.insert(ctx, in)
	if err != nil {
		return nil, err
	}
	return fc, nil
}

// Get returns a flashcard if caller can read its subject.
func (s *Service) Get(ctx context.Context, uid, id int64) (*Flashcard, error) {
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, fc.SubjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	return fc, nil
}

// ListBySubject returns all flashcards in a subject (read access required).
func (s *Service) ListBySubject(ctx context.Context, uid, subjectID int64) ([]Flashcard, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, listBySubjectSQL, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list flashcards:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// Update patches a flashcard.
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Flashcard, error) {
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return nil, err
	}
	title, question, answer := fc.Title, fc.Question, fc.Answer
	chapterID, imageID := fc.ChapterID, fc.ImageID
	if in.Title != nil {
		title = *in.Title
	}
	if in.Question != nil {
		if *in.Question == "" {
			return nil, myErrors.ErrInvalidInput
		}
		question = *in.Question
	}
	if in.Answer != nil {
		if *in.Answer == "" {
			return nil, myErrors.ErrInvalidInput
		}
		answer = *in.Answer
	}
	if in.ChapterID != nil {
		chapterID = in.ChapterID
	}
	if in.ImageID != nil {
		imageID = in.ImageID
	}
	var out Flashcard
	err = s.db.QueryRow(ctx, `
		UPDATE flashcards
		SET chapter_id=$1, title=$2, question=$3, answer=$4, image_id=$5, updated_at=now()
		WHERE id=$6
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, chapterID, title, question, answer, imageID, id).Scan(
		&out.ID, &out.SubjectID, &out.ChapterID, &out.Title, &out.Question, &out.Answer,
		&out.ImageID, &out.Source, &out.DueAt, &out.LastResult, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update flashcard:\n%w", err)
	}
	return &out, nil
}

// Delete removes a flashcard.
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
	fc, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM flashcards WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete flashcard:\n%w", err)
	}
	return nil
}

// RecordReview updates last_result/last_used and pushes a naive due_at.
// The full SRS engine is out of scope; we set due_at = now + heuristic days.
func (s *Service) RecordReview(ctx context.Context, uid, id int64, in ReviewInput) (*Flashcard, error) {
	if in.Result < 0 || in.Result > 2 {
		return nil, myErrors.ErrInvalidInput
	}
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return nil, err
	}
	due := time.Now().Add(dueDelta(in.Result))
	var out Flashcard
	err = s.db.QueryRow(ctx, `
		UPDATE flashcards
		SET last_result=$1, last_used=now(), due_at=$2, updated_at=now()
		WHERE id=$3
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, in.Result, due, id).Scan(
		&out.ID, &out.SubjectID, &out.ChapterID, &out.Title, &out.Question, &out.Answer,
		&out.ImageID, &out.Source, &out.DueAt, &out.LastResult, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("record review:\n%w", err)
	}
	return &out, nil
}

// ensureEdit returns ErrForbidden unless the caller has edit rights on the subject.
func (s *Service) ensureEdit(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanEdit() {
		return myErrors.ErrForbidden
	}
	return nil
}

// insert performs the flashcard INSERT ... RETURNING.
func (s *Service) insert(ctx context.Context, in CreateInput) (*Flashcard, error) {
	var fc Flashcard
	err := s.db.QueryRow(ctx, `
		INSERT INTO flashcards (subject_id, chapter_id, title, question, answer, image_id, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, in.SubjectID, in.ChapterID, in.Title, in.Question, in.Answer, in.ImageID, in.Source).Scan(
		&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
		&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
		&fc.CreatedAt, &fc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert flashcard:\n%w", err)
	}
	return &fc, nil
}

// load fetches a flashcard row by id, returning ErrNotFound when absent.
func (s *Service) load(ctx context.Context, id int64) (*Flashcard, error) {
	var fc Flashcard
	err := s.db.QueryRow(ctx, `
		SELECT id, subject_id, chapter_id, title, question, answer, image_id,
		       source, due_at, last_result, last_used, created_at, updated_at
		FROM flashcards WHERE id=$1
	`, id).Scan(
		&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
		&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
		&fc.CreatedAt, &fc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load flashcard:\n%w", err)
	}
	return &fc, nil
}

const listBySubjectSQL = `
SELECT id, subject_id, chapter_id, title, question, answer, image_id,
       source, due_at, last_result, last_used, created_at, updated_at
FROM flashcards WHERE subject_id=$1
ORDER BY due_at ASC NULLS FIRST, id ASC
`

// scanAll scans a pgx.Rows iterator into a slice of Flashcards.
func scanAll(rows pgx.Rows) ([]Flashcard, error) {
	var out []Flashcard
	for rows.Next() {
		var fc Flashcard
		if err := rows.Scan(
			&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
			&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
			&fc.CreatedAt, &fc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan flashcard:\n%w", err)
		}
		out = append(out, fc)
	}
	return out, rows.Err()
}

// dueDelta maps a review result to a naive due-offset.
// TODO: replace with a real SRS algorithm (SM-2 / FSRS).
func dueDelta(result int) time.Duration {
	switch result {
	case 2:
		return 72 * time.Hour
	case 1:
		return 24 * time.Hour
	default:
		return time.Hour
	}
}
