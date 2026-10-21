package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"whirlwind/internal/model"
)

type lessonEvidenceScope struct {
	Project string `json:"project_id"`
	Task    string `json:"task"`
}

func checkLessonEvidence(ctx context.Context, tx *sql.Tx, attempt model.Attempt, ids []string, independent bool) error {
	if independent && len(ids) == 0 {
		return errors.New("additional evidence required")
	}
	for _, id := range ids {
		if err := checkLessonArtifact(ctx, tx, attempt, id, independent); err != nil {
			return err
		}
	}
	return nil
}

func checkLessonArtifact(ctx context.Context, tx *sql.Tx, attempt model.Attempt, id string, independent bool) error {
	var kind, ownerKind string
	var truncated bool
	var retained int
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT a.kind,a.attempt_kind,r.data,a.truncated,a.stored_bytes FROM artifacts a JOIN resources r ON r.kind=a.attempt_kind AND r.id=a.attempt_id WHERE a.id=?`, id).Scan(&kind, &ownerKind, &data, &truncated, &retained)
	if err != nil {
		return err
	}
	if truncated || retained == 0 {
		return errors.New("complete retained evidence required")
	}
	var scope lessonEvidenceScope
	if err := json.Unmarshal(data, &scope); err != nil {
		return err
	}
	if err := checkLessonArtifactScope(ctx, tx, attempt, ownerKind, scope); err != nil {
		return err
	}
	return checkIndependentLessonKind(kind, independent)
}

func checkIndependentLessonKind(kind string, independent bool) error {
	if independent && !strings.HasPrefix(kind, "check:") && kind != "learning_source" {
		return errors.New("observed source or check evidence required")
	}
	return nil
}

func checkLessonArtifactScope(ctx context.Context, tx *sql.Tx, attempt model.Attempt, ownerKind string, scope lessonEvidenceScope) error {
	if scope.Task != attempt.Task {
		return errors.New("evidence outside task")
	}
	if ownerKind == "sessions" && scope.Project != attempt.ProjectID {
		return errors.New("evidence outside project")
	}
	if ownerKind == "verification" {
		return checkLessonTaskProject(ctx, tx, scope.Task, attempt.ProjectID)
	}
	return nil
}

func checkLessonTaskProject(ctx context.Context, tx *sql.Tx, taskID, projectID string) error {
	task, err := lessonRecord(ctx, tx, "tasks", taskID)
	if err != nil {
		return err
	}
	var value model.Task
	if err := json.Unmarshal(task.Data, &value); err != nil {
		return err
	}
	if value.ProjectID != projectID {
		return errors.New("evidence outside project")
	}
	return nil
}
