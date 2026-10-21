package workflow

import (
	"context"
	"encoding/json"
	"errors"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type lessonVectorCache struct {
	Version int       `json:"version"`
	Values  []float64 `json:"values"`
}

type missingLessonVector struct {
	record       model.Record
	key          string
	cacheVersion int
	index        int
}

func (a *agentRun) lessonVectors(ctx context.Context, records []model.Record) ([][]float64, error) {
	values := make([][]float64, len(records))
	pending := []missingLessonVector{}
	for i, record := range records {
		key := a.project.ID + ":" + record.ID + ":" + semanticConfig(a.project.Policy.Embeddings)
		vector, version, err := a.cachedLessonVector(ctx, key, record.Version)
		if err != nil {
			return nil, err
		}
		values[i] = vector
		if vector == nil {
			pending = append(pending, missingLessonVector{record: record, key: key, cacheVersion: version, index: i})
		}
	}
	for len(pending) > 0 {
		size := min(len(pending), 32)
		if err := a.embedLessonBatch(ctx, pending[:size], values); err != nil {
			return nil, err
		}
		pending = pending[size:]
	}
	return values, nil
}

func (a *agentRun) cachedLessonVector(ctx context.Context, key string, version int) ([]float64, int, error) {
	cached, err := a.Store.Get(ctx, "lesson_vectors", key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var vector lessonVectorCache
	if json.Unmarshal(cached.Data, &vector) == nil && vector.Version == version {
		return vector.Values, cached.Version, nil
	}
	return nil, cached.Version, nil
}

func (a *agentRun) embedLessonBatch(ctx context.Context, pending []missingLessonVector, values [][]float64) error {
	texts, err := lessonEmbeddingTexts(pending)
	if err != nil {
		return err
	}
	vectors, err := ctxengine.Embed(ctx, *a.project.Policy.Embeddings, texts)
	if err != nil {
		return err
	}
	changes := make([]store.Change, 0, len(pending))
	for i, entry := range pending {
		changes = append(changes, store.Change{Kind: "lesson_vectors", ID: entry.key, Value: lessonVectorCache{Version: entry.record.Version, Values: vectors[i]}, Expected: entry.cacheVersion})
	}
	if err := a.Store.Write(ctx, changes, nil); err != nil {
		return err
	}
	for i, entry := range pending {
		values[entry.index] = vectors[i]
	}
	return nil
}

func lessonEmbeddingTexts(pending []missingLessonVector) ([]string, error) {
	texts := make([]string, 0, len(pending))
	for _, entry := range pending {
		lesson, err := store.Decode[model.Lesson](entry.record)
		if err != nil {
			return nil, err
		}
		texts = append(texts, lesson.Trigger+"\n"+lesson.Mistake+"\n"+lesson.Correction)
	}
	return texts, nil
}
