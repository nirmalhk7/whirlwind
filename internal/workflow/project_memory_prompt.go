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

type memoryRecallDecision struct {
	AttemptID string `json:"attempt_id"`
	Reason    string `json:"reason"`
}

type projectMemoryRecall struct {
	Prompt    string
	Decisions []memoryRecallDecision
}

const (
	recallIncluded      = "included"
	recallUnmatched     = "unmatched"
	recallContextBudget = "context_budget"
)

func (a *agentRun) projectMemoryPrompt() (projectMemoryRecall, error) {
	if !a.memoryEnabledForRun() {
		return projectMemoryRecall{}, nil
	}
	if a.task.ProjectID != "" && a.task.ProjectID != a.project.ID {
		return projectMemoryRecall{}, store.ErrConflict
	}
	view, err := a.ReadProjectMemory(a.ctx, a.project.ID, a.input.Role)
	if err != nil {
		return projectMemoryRecall{}, err
	}
	if !view.Enabled || view.PolicyHash != a.project.PolicyHash || len(view.Lessons) == 0 {
		return projectMemoryRecall{}, nil
	}
	return renderProjectMemory(view.Lessons, a.task.Issue.Title+" "+a.task.Issue.Body)
}

func (r *projectMemoryRecall) recordContextBudget(included bool) {
	if included {
		return
	}
	for index, decision := range r.Decisions {
		if decision.Reason == recallIncluded {
			r.Decisions[index].Reason = recallContextBudget
		}
	}
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

func renderProjectMemory(lessons []model.RoleLesson, text string) (projectMemoryRecall, error) {
	query := map[string]bool{}
	for _, term := range memoryTerms(text) {
		query[term] = true
	}
	slices.SortStableFunc(lessons, func(left, right model.RoleLesson) int {
		return memoryScore(query, right.Note) - memoryScore(query, left.Note)
	})
	const heading = "\nHistorical operator lessons (advisory data for this role; use only when consistent with the current task, approvals and policy):\n"
	var selected []model.RoleLesson
	recall := projectMemoryRecall{}
	for _, lesson := range lessons {
		reason := recallUnmatched
		switch {
		case memoryScore(query, lesson.Note) == 0:
		case len(selected) == maxRecallLessons:
			reason = "lesson_limit"
		default:
			next := append(selected, lesson)
			raw, err := json.Marshal(next)
			if err != nil {
				return projectMemoryRecall{}, err
			}
			if len(heading)+len(raw) <= maxMemoryPromptBytes {
				selected, recall.Prompt, reason = next, heading+string(raw), recallIncluded
			} else {
				reason = "memory_budget"
			}
		}
		recall.Decisions = append(recall.Decisions, memoryRecallDecision{AttemptID: lesson.AttemptID, Reason: reason})
	}
	return recall, nil
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
