package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"whirlwind/internal/model"
)

func (a *agentAttempt) cleanLearningFeedback(ctx context.Context, feedback []model.LessonFeedback, snapshot *model.LearningSnapshot) []model.LessonFeedback {
	var clean []model.LessonFeedback
	for i, value := range feedback {
		if i >= 3 {
			snapshot.Warnings = append(snapshot.Warnings, "Additional feedback ignored")
			break
		}
		value, warning := a.cleanFeedbackItem(ctx, value, snapshot.Recalled)
		if warning != "" {
			snapshot.Warnings = append(snapshot.Warnings, warning)
			continue
		}
		clean = append(clean, value)
	}
	return clean
}

func validLearningFeedback(feedback model.LessonFeedback) bool {
	if feedback.Secret || feedback.IsSecret || len(feedback.Reason) > 600 {
		return false
	}
	if len(feedback.EvidenceIDs) > 8 || len(feedback.Paths) > 8 {
		return false
	}
	return !credentialLesson.MatchString(feedback.Reason + strings.Join(feedback.Paths, " "))
}

func (a *agentAttempt) cleanFeedbackItem(ctx context.Context, feedback model.LessonFeedback, refs []model.LessonRef) (model.LessonFeedback, string) {
	if !validLearningFeedback(feedback) {
		return feedback, "Invalid feedback ignored"
	}
	if !learningRecalled(refs, feedback.LessonID) {
		return feedback, "Feedback rejected: lesson was not recalled"
	}
	feedback.Anchors = map[string]string{}
	if !a.captureFeedbackSources(ctx, &feedback) {
		return feedback, "Feedback source unavailable"
	}
	return feedback, ""
}

func learningRecalled(refs []model.LessonRef, id string) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

func (a *agentAttempt) captureFeedbackSources(ctx context.Context, feedback *model.LessonFeedback) bool {
	for _, path := range feedback.Paths {
		hash, err := learningFingerprint(a.learningRoot(), path)
		if err != nil {
			return false
		}
		body, _ := json.Marshal(map[string]string{"path": path, "sha256": hash})
		artifact, err := a.Store.Capture(ctx, a.id, "learning_source", "application/json", body, 1024, false)
		if err != nil || artifact.Truncated {
			return false
		}
		feedback.EvidenceIDs = append(feedback.EvidenceIDs, artifact.ID)
		feedback.Anchors[path] = hash
	}
	return true
}
