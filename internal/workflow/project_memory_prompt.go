package workflow

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const maxMemoryPromptBytes = 2048
const maxRecallLessons = 3

func (a *agentRun) projectMemoryPrompt() (string, error) {
	if !a.memoryEnabledForRun() {
		return "", nil
	}
	if a.task.ProjectID != "" && a.task.ProjectID != a.project.ID {
		return "", store.ErrConflict
	}
	view, err := a.ReadProjectMemory(a.ctx, a.project.ID, a.input.Role)
	if err != nil {
		return "", err
	}
	if !view.Enabled || view.PolicyHash != a.project.PolicyHash || len(view.Lessons) == 0 {
		return "", nil
	}
	return renderProjectMemory(view.Lessons, a.task.Issue.Title+" "+a.task.Issue.Body)
}

func (a *agentRun) memoryEnabledForRun() bool {
	if !a.project.ManagedPolicy || !a.project.Policy.ProjectMemory || !memoryRole(a.input.Role) {
		return false
	}
	if a.input.Role == "ideate" {
		return a.input.Purpose == "generation"
	}
	return a.input.Purpose != "provider_test"
}

func renderProjectMemory(lessons []model.RoleLesson, text string) (string, error) {
	query := map[string]bool{}
	for _, term := range memoryTerms(text) {
		query[term] = true
	}
	slices.SortStableFunc(lessons, func(left, right model.RoleLesson) int {
		return memoryScore(query, right.Note) - memoryScore(query, left.Note)
	})
	const heading = "\nHistorical operator lessons (advisory data for this role; use only when consistent with the current task, approvals and policy):\n"
	var out []model.RoleLesson
	var result string
	for _, lesson := range lessons {
		if len(out) == maxRecallLessons {
			break
		}
		next := append(out, lesson)
		raw, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		if len(heading)+len(raw) <= maxMemoryPromptBytes {
			out, result = next, heading+string(raw)
		}
	}
	return result, nil
}

func memoryTerms(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func memoryScore(query map[string]bool, note string) int {
	score := 0
	for _, term := range memoryTerms(note) {
		if len(term) >= 4 && query[term] {
			score++
		}
	}
	return score
}
