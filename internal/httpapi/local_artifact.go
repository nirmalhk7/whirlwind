package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const localArtifactLimit = 512 * 1024

type localArtifactView struct {
	Metadata  model.Artifact `json:"metadata"`
	Content   string         `json:"content"`
	Truncated bool           `json:"truncated"`
}

func (s *Server) inspectArtifact(ctx context.Context, taskID, artifactID string) (json.RawMessage, error) {
	if taskID == "" || artifactID == "" {
		return nil, errors.New("task and artifact IDs are required")
	}
	artifact, content, err := s.Store.Artifact(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if err := s.artifactBelongsToTask(ctx, artifact, taskID); err != nil {
		return nil, err
	}
	truncated := len(content) > localArtifactLimit
	if truncated {
		content = store.Prefix(content, localArtifactLimit)
	}
	return json.Marshal(localArtifactView{Metadata: artifact, Content: string(content), Truncated: truncated || artifact.Truncated})
}

func (s *Server) artifactBelongsToTask(ctx context.Context, artifact model.Artifact, taskID string) error {
	var attempt struct {
		Task   string `json:"task"`
		TaskID string `json:"task_id"`
	}
	record, err := s.Store.Get(ctx, artifact.OwnerKind, artifact.AttemptID)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(record.Data, &attempt); err != nil {
		return err
	}
	if attempt.Task != taskID && attempt.TaskID != taskID {
		return store.ErrNotFound
	}
	return nil
}
