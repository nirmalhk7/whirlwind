package workflow

import (
	"context"
	"slices"
	"strings"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type semanticMatch struct {
	record model.Record
	score  float64
}

func (a *agentRun) semanticLessons(ctx context.Context, query string, lexical []model.Record) []model.Record {
	if !a.semanticSearchAllowed(ctx, query) {
		return lexical
	}
	matches, err := a.semanticMatches(ctx, query)
	if err != nil {
		a.warnSemantic("Semantic lesson search unavailable; used text search")
		return lexical
	}
	return fuseLessons(lexical, matches, query)
}

func (a *agentRun) semanticSearchAllowed(ctx context.Context, query string) bool {
	config := a.project.Policy.Embeddings
	if config == nil || !config.LessonsEnabled {
		return false
	}
	if credentialLesson.MatchString(query) {
		a.warnSemantic("Sensitive query kept local; used text search")
		return false
	}
	record, err := a.Store.Get(ctx, "lesson_semantic_evaluations", a.project.ID+":"+semanticConfig(config))
	if err != nil {
		return false
	}
	evaluation, err := store.Decode[LessonEvaluation](record)
	return err == nil && evaluation.Enabled
}

func (a *agentRun) warnSemantic(message string) {
	if a.learning != nil {
		a.learning.Warnings = append(a.learning.Warnings, message)
	}
}

func (a *agentRun) semanticMatches(ctx context.Context, query string) ([]semanticMatch, error) {
	records, err := a.semanticRecords(ctx)
	if err != nil {
		return nil, err
	}
	vector, err := a.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	candidates, err := a.lessonVectors(ctx, records)
	if err != nil {
		return nil, err
	}
	matches := []semanticMatch{}
	for i, record := range records {
		score := ctxengine.Cosine(vector, candidates[i])
		if score >= .75 {
			matches = append(matches, semanticMatch{record, score})
		}
	}
	slices.SortFunc(matches, compareSemanticMatches)
	return matches[:min(len(matches), 32)], nil
}

func compareSemanticMatches(a, b semanticMatch) int {
	if a.score > b.score {
		return -1
	}
	if a.score < b.score {
		return 1
	}
	return strings.Compare(a.record.ID, b.record.ID)
}
