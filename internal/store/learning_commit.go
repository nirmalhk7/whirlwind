package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

type learningCommit struct {
	ctx      context.Context
	tx       *sql.Tx
	attempt  model.Attempt
	snapshot *model.LearningSnapshot
}

// CommitLearning applies reports once, with lesson changes and audit events in one transaction.
func (s *Store) CommitLearning(ctx context.Context, attempt model.Attempt, report model.LearningReport) (*model.LearningSnapshot, error) {
	snapshot := initialLearningSnapshot(attempt)
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		old, err := lessonRecord(ctx, tx, "learning_reports", attempt.AttemptID)
		if err == nil {
			return json.Unmarshal(old.Data, &snapshot)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		commit := learningCommit{ctx: ctx, tx: tx, attempt: attempt, snapshot: &snapshot}
		if err := commit.applyReport(report); err != nil {
			return err
		}
		return put(ctx, tx, "learning_reports", attempt.AttemptID, snapshot, 0)
	})
	return &snapshot, err
}

func initialLearningSnapshot(attempt model.Attempt) model.LearningSnapshot {
	if attempt.Learning == nil {
		return model.LearningSnapshot{Recalled: []model.LessonRef{}}
	}
	snapshot := *attempt.Learning
	snapshot.Warnings = append([]string{}, attempt.Learning.Warnings...)
	return snapshot
}

func (c learningCommit) applyReport(report model.LearningReport) error {
	for _, candidate := range report.Lessons {
		if err := c.recordCandidate(candidate); err != nil {
			return err
		}
	}
	for _, feedback := range report.Feedback {
		if err := c.recordFeedback(feedback); err != nil {
			return err
		}
	}
	return nil
}

func (c learningCommit) warn(message string) {
	c.snapshot.Warnings = append(c.snapshot.Warnings, message)
}

func (c learningCommit) recordEvidence(lesson string, ids []string) error {
	for _, id := range ids {
		if err := recordLessonEvidence(c.ctx, c.tx, lesson, id); err != nil {
			return err
		}
	}
	return nil
}
