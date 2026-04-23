package aipipeline

import (
	"context"
	"fmt"
)

// ReapOrphanedJobs flips ai_jobs rows in status='running' that started > 1h ago
// to status='failed' with error_kind='orphaned'. Returns the number of rows flipped.
// Designed to be registered as a cron.Job.
func (s *Service) ReapOrphanedJobs(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, sqlReapOrphanJobs)
	if err != nil {
		return 0, fmt.Errorf("reap orphaned jobs:\n%w", err)
	}
	return tag.RowsAffected(), nil
}
