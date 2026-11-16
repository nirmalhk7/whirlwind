package store

import (
	"context"
	"database/sql"
	"whirlwind/internal/model"
)

func (s *Store) IdeationProgress(ctx context.Context, run string) ([]model.IdeationDirectionProgress, error) {
	return ReadIdeationProgress(ctx, s.DB, run)
}

func ReadIdeationProgress(ctx context.Context, db *sql.DB, run string) ([]model.IdeationDirectionProgress, error) {
	rows, err := db.QueryContext(ctx, "SELECT direction,candidate_count,status,COALESCE(idea_id,''),error_reason FROM ideation_directions WHERE run_id=? ORDER BY direction", run)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	progress := []model.IdeationDirectionProgress{}
	for rows.Next() {
		var direction model.IdeationDirectionProgress
		if err := rows.Scan(&direction.Direction, &direction.CandidateCount, &direction.Status, &direction.IdeaID, &direction.ErrorReason); err != nil {
			return nil, err
		}
		progress = append(progress, direction)
	}
	return progress, rows.Err()
}
