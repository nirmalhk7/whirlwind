package store

import (
	"slices"
	"whirlwind/internal/model"
)

func applyLessonVerdict(lesson *model.Lesson, feedback model.LessonFeedback) bool {
	switch feedback.Verdict {
	case "supports":
		lesson.Status = "corroborated"
		if lesson.Anchors == nil {
			lesson.Anchors = map[string]string{}
		}
		for path, hash := range feedback.Anchors {
			lesson.Anchors[path] = hash
		}
	case "contradicts", "supersedes":
		lesson.Status = "retired"
	default:
		return false
	}
	return true
}

func recentLessonEvidence(ids []string) []string {
	// Retain bounded recent evidence; the audit preserves older references.
	seen := map[string]bool{}
	recent := []string{}
	for i := len(ids) - 1; i >= 0 && len(recent) < 8; i-- {
		id := ids[i]
		if !seen[id] {
			recent = append(recent, id)
			seen[id] = true
		}
	}
	slices.Reverse(recent)
	return recent
}
