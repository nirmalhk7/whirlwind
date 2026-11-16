package store

import (
	"context"
	"database/sql"
	"errors"
	"whirlwind/internal/model"
)

// CompleteResourceArtifact reuses a complete immutable payload after a lost
// handoff write; truncated previews never stand in for source patch bytes.
func (s *Store) CompleteResourceArtifact(ctx context.Context, owner ArtifactOwner, kind, hash string) (model.Artifact, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE attempt_kind=? AND attempt_id=? AND kind=? AND sha256=? AND truncated=0 AND stored_bytes=bytes ORDER BY created_at,id LIMIT 1`, owner.Kind, owner.ID, kind, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Artifact{}, ErrNotFound
	}
	if err != nil {
		return model.Artifact{}, err
	}
	return s.ArtifactMetadata(ctx, id)
}
