package workflow

import (
	"context"
	"time"
	"whirlwind/internal/model"
)

func (a *agentAttempt) finishLearning(ctx context.Context, raw string, runErr error) string {
	if a.learning == nil {
		return raw
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, report, warnings := decodeLearning(raw)
	snapshot := *a.learning
	snapshot.Warnings = append(append([]string{}, a.learning.Warnings...), warnings...)
	a.state.Learning = &snapshot
	// Failed transports may contain partial or misleading output. Preserve it as evidence only.
	if runErr != nil {
		return result
	}
	clean := a.cleanLearningReport(ctx, report, &snapshot)
	stored, err := a.Store.CommitLearning(ctx, a.state, clean)
	if err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Learning update unavailable")
		return result
	}
	a.state.Learning = stored
	return result
}

func (a *agentAttempt) cleanLearningReport(ctx context.Context, report model.LearningReport, snapshot *model.LearningSnapshot) model.LearningReport {
	clean := model.LearningReport{}
	for i, lesson := range report.Lessons {
		if i >= 2 {
			snapshot.Warnings = append(snapshot.Warnings, "Additional lessons ignored")
			break
		}
		value, warning := a.cleanLearningLesson(lesson)
		if warning != "" {
			snapshot.Warnings = append(snapshot.Warnings, warning)
			continue
		}
		clean.Lessons = append(clean.Lessons, value)
	}
	clean.Feedback = a.cleanLearningFeedback(ctx, report.Feedback, snapshot)
	return clean
}
