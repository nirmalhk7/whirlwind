package workflow

import (
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentRun) currentLabeledLesson(records []model.Record, id string) bool {
	for _, record := range records {
		if record.ID == id {
			lesson, err := store.Decode[model.Lesson](record)
			return err == nil && a.lessonCurrent(lesson)
		}
	}
	return false
}

func containsLesson(records []model.Record, id string) bool {
	for _, record := range records {
		if record.ID == id {
			return true
		}
	}
	return false
}
