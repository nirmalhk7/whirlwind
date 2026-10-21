package store

import (
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

func (c learningCommit) recordCandidate(candidate model.Lesson) error {
	candidate.ProjectID = c.attempt.ProjectID
	candidate.Role = c.attempt.Role
	candidate.AttemptID = c.attempt.AttemptID
	candidate.TaskID = c.attempt.Task
	candidate.Provider = c.attempt.Provider
	candidate.Model = c.attempt.Model
	candidate.Status = "tentative"
	if err := checkLessonEvidence(c.ctx, c.tx, c.attempt, candidate.EvidenceIDs, false); err != nil {
		c.warn("Lesson evidence rejected")
		return nil
	}
	candidate.ID = lessonID(candidate)
	saved, err := c.saveCandidate(candidate)
	if err != nil || !saved {
		return err
	}
	c.snapshot.CreatedIDs = append(c.snapshot.CreatedIDs, candidate.ID)
	if err := c.recordEvidence(candidate.ID, candidate.EvidenceIDs); err != nil {
		return err
	}
	return lessonAudit(c.ctx, c.tx, c.attempt, "lesson_recorded", candidate.ID, "")
}

func (c learningCommit) saveCandidate(candidate model.Lesson) (bool, error) {
	previous, err := lessonRecord(c.ctx, c.tx, "lessons", candidate.ID)
	if errors.Is(err, ErrNotFound) {
		return true, put(c.ctx, c.tx, "lessons", candidate.ID, candidate, 0)
	}
	if err != nil {
		return false, err
	}
	var saved model.Lesson
	if err := json.Unmarshal(previous.Data, &saved); err != nil {
		return false, err
	}
	// Repetition is not independent confirmation. Retired advice never revives implicitly.
	if saved.Status != "stale" {
		return false, nil
	}
	return true, put(c.ctx, c.tx, "lessons", candidate.ID, candidate, previous.Version)
}
