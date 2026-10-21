package workflow

import (
	"context"
	"errors"
	"slices"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

type LessonFixture struct {
	Role     string   `json:"role"`
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
}
type LessonEvaluation struct {
	Enabled           bool    `json:"enabled"`
	BaselineRecall    float64 `json:"baseline_recall"`
	Recall            float64 `json:"recall"`
	BaselinePrecision float64 `json:"baseline_precision"`
	Precision         float64 `json:"precision"`
	ExactCoverage     bool    `json:"exact_coverage"`
	Configuration     string  `json:"configuration"`
}

func (e *Engine) EvaluateLessonSearch(ctx context.Context, p model.Project, fixtures []LessonFixture) (LessonEvaluation, error) {
	result := LessonEvaluation{ExactCoverage: true}
	if p.Policy.Embeddings == nil || !p.Policy.Embeddings.LessonsEnabled || len(fixtures) == 0 || len(fixtures) > 100 {
		return result, errors.New("configured semantic lesson search and 1–100 labeled fixtures required")
	}
	for _, fixture := range fixtures {
		if !slices.Contains(model.AgentRoles(), fixture.Role) || fixture.Query == "" || len(fixture.Query) > 2048 || credentialLesson.MatchString(fixture.Query) {
			return result, errors.New("invalid lesson search fixture")
		}
		run := &agentRun{Engine: e, ctx: ctx, project: p, input: provider.Input{Role: fixture.Role, CWD: p.Path}}
		if err := run.evaluateLessonFixture(ctx, fixture, &result); err != nil {
			return result, err
		}
	}
	n := float64(len(fixtures))
	result.BaselineRecall /= n
	result.Recall /= n
	result.BaselinePrecision /= n
	result.Precision /= n
	result.Enabled = result.Recall > result.BaselineRecall && result.Precision >= result.BaselinePrecision && result.ExactCoverage
	result.Configuration = semanticConfig(p.Policy.Embeddings)
	err := e.saveLessonEvaluation(ctx, p.ID, result)
	return result, err
}

func (a *agentRun) evaluateLessonFixture(ctx context.Context, fixture LessonFixture, result *LessonEvaluation) error {
	records, err := a.semanticRecords(ctx)
	if err != nil {
		return err
	}
	for _, id := range fixture.Relevant {
		if !a.currentLabeledLesson(records, id) {
			return errors.New("fixture lesson outside active project/role memory")
		}
	}
	baseline, err := a.Store.MatchingLessons(ctx, a.project.ID, fixture.Role, fixture.Query)
	if err != nil {
		return err
	}
	matches, err := a.semanticMatches(ctx, fixture.Query)
	if err != nil {
		return err
	}
	hybrid := fuseLessons(baseline, matches, fixture.Query)
	br, bp := lessonSearchScore(baseline, fixture.Relevant)
	hr, hp := lessonSearchScore(hybrid, fixture.Relevant)
	result.BaselineRecall += br
	result.BaselinePrecision += bp
	result.Recall += hr
	result.Precision += hp
	for _, r := range baseline[:min(len(baseline), 3)] {
		if exactLessonMatch(r, fixture.Query) > 0 && !containsLesson(hybrid[:min(len(hybrid), 3)], r.ID) {
			result.ExactCoverage = false
		}
	}
	return nil
}
