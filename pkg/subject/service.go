package subject

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns subject CRUD and listing.
type Service struct {
	db     *pgxpool.Pool   // db is the shared connection pool
	access *access.Service // access resolves visibility and membership
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a new subject owned by uid.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Subject, error) {
	if in.Name == "" {
		return nil, myErrors.ErrInvalidInput
	}
	vis := in.Visibility
	if vis == "" {
		vis = "private"
	}
	if vis != "private" && vis != "friends" && vis != "public" {
		return nil, myErrors.ErrInvalidInput
	}
	var sub Subject
	err := s.db.QueryRow(ctx, `
		INSERT INTO subjects (owner_id, name, color, icon, tags, visibility, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, uid, in.Name, in.Color, in.Icon, in.Tags, vis, in.Description).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert subject:\n%w", err)
	}
	return &sub, nil
}

// Get fetches a subject if the user can read it.
func (s *Service) Get(ctx context.Context, uid, subjectID int64) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	return sub, nil
}

// Stats returns the per-difficulty card distribution for a subject the caller can read.
func (s *Service) Stats(ctx context.Context, uid, subjectID int64) (*StatsResponse, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	out := &StatsResponse{}
	err = s.db.QueryRow(ctx, `
		SELECT
			COUNT(*)                                   AS total,
			COUNT(*) FILTER (WHERE last_result = 2)    AS good,
			COUNT(*) FILTER (WHERE last_result = 1)    AS ok,
			COUNT(*) FILTER (WHERE last_result = 0)    AS bad,
			COUNT(*) FILTER (WHERE last_result = -1)   AS new_count
		FROM flashcards
		WHERE subject_id = $1
	`, subjectID).Scan(&out.TotalCards, &out.GoodCount, &out.OkCount, &out.BadCount, &out.NewCount)
	if err != nil {
		return nil, fmt.Errorf("subject stats:\n%w", err)
	}
	out.CardsStudied = out.TotalCards - out.NewCount
	if out.TotalCards > 0 {
		out.MasteryPercent = (float64(out.GoodCount) + float64(out.OkCount)*0.5) / float64(out.TotalCards)
	}
	return out, nil
}

// ListOwned returns all subjects owned by uid, excluding archived when includeArchived=false.
func (s *Service) ListOwned(ctx context.Context, uid int64, includeArchived bool) ([]Subject, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects
		WHERE owner_id = $1 AND ($2 OR archived = false)
		ORDER BY last_used DESC NULLS LAST, id DESC
	`, uid, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("list subjects:\n%w", err)
	}
	defer rows.Close()
	return scanSubjects(rows)
}

// Update patches a subject; requires the caller to be owner.
func (s *Service) Update(ctx context.Context, uid, subjectID int64, in UpdateInput) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	if sub.OwnerID != uid {
		return nil, myErrors.ErrForbidden
	}
	name, color, icon, tags, vis, desc, archived, err := applySubjectPatch(sub, in)
	if err != nil {
		return nil, err
	}
	var out Subject
	err = s.db.QueryRow(ctx, `
		UPDATE subjects
		SET name=$1, color=$2, icon=$3, tags=$4, visibility=$5,
		    description=$6, archived=$7, updated_at=now()
		WHERE id=$8
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, name, color, icon, tags, vis, desc, archived, subjectID).Scan(
		&out.ID, &out.OwnerID, &out.Name, &out.Color, &out.Icon, &out.Tags,
		&out.Visibility, &out.Archived, &out.Description, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update subject:\n%w", err)
	}
	return &out, nil
}

// applySubjectPatch merges UpdateInput fields onto the existing Subject values.
// Returns the patched field values or ErrInvalidInput for constraint violations.
func applySubjectPatch(sub *Subject, in UpdateInput) (name, color, icon, tags, vis, desc string, archived bool, err error) {
	name, color, icon = sub.Name, sub.Color, sub.Icon
	tags, vis, desc, archived = sub.Tags, sub.Visibility, sub.Description, sub.Archived
	if in.Name != nil {
		if *in.Name == "" {
			return "", "", "", "", "", "", false, myErrors.ErrInvalidInput
		}
		name = *in.Name
	}
	if in.Color != nil {
		color = *in.Color
	}
	if in.Icon != nil {
		icon = *in.Icon
	}
	if in.Tags != nil {
		tags = *in.Tags
	}
	if in.Visibility != nil {
		v := *in.Visibility
		if v != "private" && v != "friends" && v != "public" {
			return "", "", "", "", "", "", false, myErrors.ErrInvalidInput
		}
		vis = v
	}
	if in.Description != nil {
		desc = *in.Description
	}
	if in.Archived != nil {
		archived = *in.Archived
	}
	return name, color, icon, tags, vis, desc, archived, nil
}

// Delete removes a subject; requires the caller to be owner.
func (s *Service) Delete(ctx context.Context, uid, subjectID int64) error {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return err
	}
	if sub.OwnerID != uid {
		return myErrors.ErrForbidden
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM subjects WHERE id=$1`, subjectID); err != nil {
		return fmt.Errorf("delete subject:\n%w", err)
	}
	return nil
}

// TouchLastUsed sets last_used=now() on the subject (called by training/quiz flows later).
func (s *Service) TouchLastUsed(ctx context.Context, subjectID int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE subjects SET last_used = now() WHERE id = $1`, subjectID); err != nil {
		return fmt.Errorf("touch subject last_used:\n%w", err)
	}
	return nil
}

func (s *Service) load(ctx context.Context, id int64) (*Subject, error) {
	var sub Subject
	err := s.db.QueryRow(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects WHERE id=$1
	`, id).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load subject:\n%w", err)
	}
	return &sub, nil
}

func scanSubjects(rows pgx.Rows) ([]Subject, error) {
	var out []Subject
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(
			&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
			&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
			&sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subject:\n%w", err)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}
