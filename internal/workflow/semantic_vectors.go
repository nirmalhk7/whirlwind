package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func semanticConfig(config *model.Embeddings) string {
	b, _ := json.Marshal([]string{config.Endpoint, config.Model, config.KeyEnv})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *agentRun) semanticRecords(ctx context.Context) ([]model.Record, error) {
	all := []model.Record{}
	var after int64
	for {
		page, err := a.Store.LessonsPage(ctx, a.project.ID, a.input.Role, "", after, 500)
		if err != nil {
			return nil, err
		}
		for _, record := range page.Items {
			lesson, err := store.Decode[model.Lesson](record)
			if err == nil && (lesson.Status == "tentative" || lesson.Status == "corroborated") {
				all = append(all, record)
			}
		}
		if len(page.Items) < 500 {
			break
		}
		after = page.NextAfter
	}
	return all, nil
}

func (a *agentRun) queryVector(ctx context.Context, query string) ([]float64, error) {
	config := a.project.Policy.Embeddings
	sum := sha256.Sum256([]byte(query))
	key := a.project.ID + ":" + semanticConfig(config) + ":" + hex.EncodeToString(sum[:])
	cached, err := a.Store.Get(ctx, "lesson_queries", key)
	if err == nil {
		return store.Decode[[]float64](cached)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	vectors, err := ctxengine.Embed(ctx, *config, []string{string(store.Prefix([]byte(query), 2048))})
	if err != nil {
		return nil, err
	}
	err = a.Store.Put(ctx, "lesson_queries", key, vectors[0], 0)
	return vectors[0], err
}
