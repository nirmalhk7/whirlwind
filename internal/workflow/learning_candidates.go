package workflow

import (
	"regexp"
	"strings"
	"whirlwind/internal/model"
)

var credentialLesson = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:password|api[_-]?key|access[_-]?token|secret)\s*[:=]\s*\S+|-----BEGIN .*PRIVATE KEY)`)

func validLearningLesson(lesson model.Lesson) bool {
	if lesson.Secret || lesson.IsSecret || strings.TrimSpace(lesson.Trigger) == "" {
		return false
	}
	if strings.TrimSpace(lesson.Mistake) == "" || strings.TrimSpace(lesson.Correction) == "" {
		return false
	}
	text := lesson.Trigger + lesson.Mistake + lesson.Correction + lesson.ErrorSignature
	if len(text) > 600 || len(lesson.Paths) > 8 || len(lesson.Symbols) > 8 || len(lesson.EvidenceIDs) > 8 {
		return false
	}
	return !credentialLesson.MatchString(text + strings.Join(lesson.Paths, " ") + strings.Join(lesson.Symbols, " "))
}

func (a *agentAttempt) cleanLearningLesson(lesson model.Lesson) (model.Lesson, string) {
	if !validLearningLesson(lesson) {
		return lesson, "Invalid or sensitive lesson ignored"
	}
	anchors, valid := learningAnchors(a.learningRoot(), lesson.Paths)
	lesson.Anchors = anchors
	if !valid || !validLearningSymbols(lesson.Symbols) || len(anchors) == 0 && len(lesson.EvidenceIDs) == 0 {
		return lesson, "Lesson requires accessible source or evidence"
	}
	// Do not accept model-authored lifecycle or provenance fields.
	lesson.Reason = ""
	return lesson, ""
}

func learningAnchors(root string, paths []string) (map[string]string, bool) {
	anchors := map[string]string{}
	for _, path := range paths {
		hash, err := learningFingerprint(root, path)
		if err != nil {
			return anchors, false
		}
		anchors[path] = hash
	}
	return anchors, true
}

func validLearningSymbols(symbols []string) bool {
	for _, symbol := range symbols {
		if len(symbol) > 128 {
			return false
		}
	}
	return true
}
