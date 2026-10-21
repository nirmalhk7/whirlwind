package store

import (
	"encoding/json"
	"errors"
	"slices"
	"whirlwind/internal/model"
)

func (c learningCommit) recordFeedback(feedback model.LessonFeedback) error {
	record, lesson, warning, err := c.feedbackLesson(feedback.LessonID)
	if err != nil || warning != "" {
		if warning != "" {
			c.warn(warning)
		}
		return err
	}
	reused, err := c.reusedEvidence(lesson, feedback.EvidenceIDs)
	if err != nil {
		return err
	}
	if err := checkLessonEvidence(c.ctx, c.tx, c.attempt, feedback.EvidenceIDs, true); err != nil || reused {
		c.warn("Feedback rejected: additional evidence required")
		return nil
	}
	if !applyLessonVerdict(&lesson, feedback) {
		c.warn("Feedback rejected: unknown verdict")
		return nil
	}
	return c.saveFeedback(record, lesson, feedback)
}

func (c learningCommit) feedbackLesson(id string) (model.Record, model.Lesson, string, error) {
	var lesson model.Lesson
	ref, recalled := c.recalledLesson(id)
	if !recalled {
		return model.Record{}, lesson, "Feedback rejected: lesson was not recalled", nil
	}
	record, err := lessonRecord(c.ctx, c.tx, "lessons", id)
	if err != nil {
		return record, lesson, "", err
	}
	if err := json.Unmarshal(record.Data, &lesson); err != nil {
		return record, lesson, "", err
	}
	if lesson.ProjectID != c.attempt.ProjectID || lesson.Role != c.attempt.Role || lesson.Status == "retired" || record.Version != ref.Version {
		return record, lesson, "Feedback rejected: scope or version changed", nil
	}
	return record, lesson, "", nil
}

func (c learningCommit) reusedEvidence(lesson model.Lesson, ids []string) (bool, error) {
	reused := false
	for _, id := range ids {
		if slices.Contains(lesson.EvidenceIDs, id) {
			reused = true
		}
		_, err := lessonRecord(c.ctx, c.tx, "lesson_evidence", lessonEvidenceID(lesson.ID, id))
		if err == nil {
			reused = true
		} else if !errors.Is(err, ErrNotFound) {
			return false, err
		}
	}
	return reused, nil
}

func (c learningCommit) saveFeedback(record model.Record, lesson model.Lesson, feedback model.LessonFeedback) error {
	lesson.Reason = feedback.Reason
	lesson.EvidenceIDs = recentLessonEvidence(append(lesson.EvidenceIDs, feedback.EvidenceIDs...))
	if err := put(c.ctx, c.tx, "lessons", lesson.ID, lesson, record.Version); err != nil {
		return err
	}
	if err := c.recordEvidence(lesson.ID, feedback.EvidenceIDs); err != nil {
		return err
	}
	if err := lessonAudit(c.ctx, c.tx, c.attempt, "lesson_"+feedback.Verdict, lesson.ID, feedback.Reason); err != nil {
		return err
	}
	c.snapshot.Feedback = append(c.snapshot.Feedback, feedback)
	return nil
}

func (c learningCommit) recalledLesson(id string) (model.LessonRef, bool) {
	for _, ref := range c.snapshot.Recalled {
		if ref.ID == id {
			return ref, true
		}
	}
	return model.LessonRef{}, false
}
