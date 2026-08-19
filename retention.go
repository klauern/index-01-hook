package main

import (
	"context"
	"fmt"
	"time"
)

const terminalRecordRetention = 30 * 24 * time.Hour

func (s *Store) PurgeExpiredRecordings(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, fmt.Errorf("retention must be positive")
	}
	cutoff := timestamp(s.now().UTC().Add(-retention))
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM recordings
		WHERE id IN (
			SELECT r.id
			FROM recordings r
			LEFT JOIN extraction_jobs j ON j.recording_id = r.id
			WHERE r.last_received_at < ?
				AND (
					j.recording_id IS NULL
					OR (
						j.workflow_state NOT IN ('received', 'extracting', 'retry_wait')
						AND j.updated_at < ?
						AND NOT EXISTS (
							SELECT 1 FROM delivery_tasks active
							WHERE active.recording_id = r.id
								AND active.workflow_state IN ('extracted', 'creating', 'retry_wait')
						)
						AND NOT EXISTS (
							SELECT 1 FROM delivery_tasks recent
							WHERE recent.recording_id = r.id AND recent.updated_at >= ?
						)
					)
				)
		)`, cutoff, cutoff, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge expired recordings: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read purged recording count: %w", err)
	}
	return count, nil
}
