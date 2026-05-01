package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// persist replaces any existing plan for examID with a freshly generated one.
// Runs DELETE + INSERT in a single transaction so a failed insert leaves
// the previous plan intact.
func (s *Service) persist(ctx context.Context, examID int64, days []Day, model, promptHash string) (*Plan, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM revision_plans WHERE exam_id = $1`, examID); err != nil {
		return nil, fmt.Errorf("delete prior plan:\n%w", err)
	}
	daysJSON, err := json.Marshal(days)
	if err != nil {
		return nil, fmt.Errorf("marshal days:\n%w", err)
	}
	plan := Plan{ExamID: examID, Days: days, Model: model, PromptHash: promptHash}
	row := tx.QueryRow(ctx, `
        INSERT INTO revision_plans (exam_id, days, model, prompt_hash)
        VALUES ($1, $2, $3, $4)
        RETURNING id, generated_at
    `, examID, daysJSON, model, promptHash)
	if err := row.Scan(&plan.ID, &plan.GeneratedAt); err != nil {
		return nil, fmt.Errorf("insert plan:\n%w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit plan:\n%w", err)
	}
	return &plan, nil
}

// loadPlanByExam returns the stored plan for examID, or pgx.ErrNoRows if none exists.
func (s *Service) loadPlanByExam(ctx context.Context, examID int64) (*Plan, error) {
	var p Plan
	var daysRaw []byte
	err := s.db.QueryRow(ctx, `
        SELECT id, exam_id, days, model, prompt_hash, generated_at
        FROM revision_plans WHERE exam_id = $1
    `, examID).Scan(&p.ID, &p.ExamID, &daysRaw, &p.Model, &p.PromptHash, &p.GeneratedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(daysRaw, &p.Days); err != nil {
		return nil, fmt.Errorf("unmarshal days:\n%w", err)
	}
	return &p, nil
}

// loadProgressByDate returns a map of "YYYY-MM-DD" → set of FC IDs the user
// marked done on that date. Used by the drift calculator and today bucket.
func (s *Service) loadProgressByDate(ctx context.Context, userID int64) (map[string]map[int64]bool, error) {
	rows, err := s.db.Query(ctx, `
        SELECT plan_date, fc_id FROM revision_plan_progress WHERE user_id = $1
    `, userID)
	if err != nil {
		return nil, fmt.Errorf("load progress:\n%w", err)
	}
	defer rows.Close()
	out := map[string]map[int64]bool{}
	for rows.Next() {
		var date string
		var fcID int64
		if err := rows.Scan(&date, &fcID); err != nil {
			return nil, fmt.Errorf("scan progress:\n%w", err)
		}
		if out[date] == nil {
			out[date] = map[int64]bool{}
		}
		out[date][fcID] = true
	}
	return out, rows.Err()
}

// hashPrompt produces a deterministic short hex digest of the rendered prompt
// so revision_plans.prompt_hash can correlate plans with the prompt revision
// that produced them.
func hashPrompt(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// notFound returns true when err is pgx.ErrNoRows.
func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
