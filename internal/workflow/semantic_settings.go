package workflow

import (
	"context"
	"errors"
	"slices"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) saveLessonEvaluation(ctx context.Context, project string, result LessonEvaluation) error {
	id := project + ":" + result.Configuration
	previous, err := e.Store.Get(ctx, "lesson_semantic_evaluations", id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return e.Store.Put(ctx, "lesson_semantic_evaluations", id, result, previous.Version)
}

func lessonSearchScore(records []model.Record, relevant []string) (float64, float64) {
	hits := 0
	records = records[:min(len(records), 3)]
	for _, r := range records {
		if slices.Contains(relevant, r.ID) {
			hits++
		}
	}
	if len(relevant) == 0 {
		if len(records) == 0 {
			return 1, 1
		}
		return 0, 0
	}
	return float64(hits) / float64(len(relevant)), float64(hits) / float64(max(len(records), 1))
}
