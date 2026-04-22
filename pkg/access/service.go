package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Level is the caller's effective access level on a subject.
type Level int

const (
	LevelNone   Level = iota // LevelNone means no access
	LevelViewer              // LevelViewer can read
	LevelEditor              // LevelEditor can modify chapters/flashcards
	LevelOwner               // LevelOwner can manage the subject itself
)

// CanRead reports whether the level is allowed to read the subject.
func (l Level) CanRead() bool { return l >= LevelViewer }

// CanEdit reports whether the level is allowed to modify chapters / flashcards.
func (l Level) CanEdit() bool { return l >= LevelEditor }

// CanManage reports whether the level is allowed to modify the subject itself
// (rename, delete, share, manage collaborators).
func (l Level) CanManage() bool { return l >= LevelOwner }

// Service answers AI-entitlement and per-subject access questions.
type Service struct {
	db *pgxpool.Pool
}

// NewService constructs the access service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// HasAIAccess returns true when the user has an active AI subscription.
// Source of truth is the user_has_ai_access SQL function.
func (s *Service) HasAIAccess(ctx context.Context, uid int64) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT user_has_ai_access($1)`, uid).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check ai access:\n%w", err)
	}
	return ok, nil
}

// SubjectLevel resolves the caller's effective access level on a subject.
// Resolution: owner → collaborator role → friend-of-owner (if visibility='friends')
//
//	→ subscriber (if visibility='public') → none.
func (s *Service) SubjectLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	lvl, err := s.ownerLevel(ctx, uid, subjectID)
	if err != nil || lvl != LevelNone {
		return lvl, err
	}
	lvl, err = s.collaboratorLevel(ctx, uid, subjectID)
	if err != nil || lvl != LevelNone {
		return lvl, err
	}
	return s.visibilityLevel(ctx, uid, subjectID)
}

func (s *Service) ownerLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var ownerID int64
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM subjects WHERE id = $1`, subjectID).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LevelNone, nil
		}
		return LevelNone, fmt.Errorf("load subject owner:\n%w", err)
	}
	if ownerID == uid {
		return LevelOwner, nil
	}
	return LevelNone, nil
}

func (s *Service) collaboratorLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT role FROM collaborators WHERE subject_id = $1 AND user_id = $2`,
		subjectID, uid).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return LevelNone, nil
	}
	if err != nil {
		return LevelNone, fmt.Errorf("load collaborator:\n%w", err)
	}
	if role == "editor" {
		return LevelEditor, nil
	}
	return LevelViewer, nil
}

func (s *Service) visibilityLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var visibility string
	var ownerID int64
	err := s.db.QueryRow(ctx,
		`SELECT visibility, owner_id FROM subjects WHERE id = $1`,
		subjectID).Scan(&visibility, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LevelNone, nil
	}
	if err != nil {
		return LevelNone, fmt.Errorf("load subject visibility:\n%w", err)
	}
	switch visibility {
	case "public":
		return LevelViewer, nil
	case "friends":
		if isFriend, err := s.isFriend(ctx, uid, ownerID); err != nil {
			return LevelNone, err
		} else if isFriend {
			return LevelViewer, nil
		}
	}
	return LevelNone, nil
}

func (s *Service) isFriend(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT count(*) FROM friendships
        WHERE status = 'accepted' AND (
          (sender_id = $1 AND receiver_id = $2) OR
          (sender_id = $2 AND receiver_id = $1)
        )`, a, b).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check friendship:\n%w", err)
	}
	return n > 0, nil
}
