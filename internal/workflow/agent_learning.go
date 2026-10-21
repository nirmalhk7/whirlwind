package workflow

import (
	"slices"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/prompts/agents"
)

func (a *agentRun) prepareLearning() {
	if !slices.Contains(model.AgentRoles(), a.input.Role) || a.input.Purpose == "provider_test" {
		return
	}
	a.learning = &model.LearningSnapshot{Recalled: []model.LessonRef{}}
	defer a.appendLearningInstructions()
	query := a.taskLearningQuery()
	budget, err := a.Store.MemoryAllowance(a.ctx, a.memoryKey())
	if err != nil {
		a.learning.Warnings = append(a.learning.Warnings, "Learning budget unavailable")
		return
	}
	a.learning.SearchCalls = budget.Calls
	a.learning.SearchBytes = budget.Bytes
	block, refs, err := a.lessonBlockWithin(a.ctx, query, budget.Seen, 2048-budget.AutomaticBytes)
	if err != nil {
		a.learning.Warnings = append(a.learning.Warnings, "Learning recall unavailable")
	}
	if len(refs) == 0 {
		return
	}
	if err := a.Store.RecordAutomaticMemory(a.ctx, a.memoryKey(), refs, len(block)); err != nil {
		a.learning.Warnings = append(a.learning.Warnings, "Learning budget update unavailable")
		return
	}
	a.input.Prompt += block
	a.learning.Recalled = refs
	a.learning.Bytes = len(block)
	a.learning.EstimatedTokens = (len(block) + 3) / 4
}

func (a *agentRun) taskLearningQuery() string {
	facts := []string{a.task.Issue.Title, a.task.Issue.Body, a.task.Handoff.NextAction}
	facts = append(facts, a.task.Handoff.FailingTests...)
	return strings.Join(facts, " ")
}

func (a *agentRun) appendLearningInstructions() {
	instructions, _ := agents.Render("learning.md", nil)
	a.input.Prompt += "\n" + instructions
}

func (a *agentRun) lessonCurrent(l model.Lesson) bool {
	root := a.learningRoot()
	for path, hash := range l.Anchors {
		current, err := learningFingerprint(root, path)
		if err != nil || current != hash {
			return false
		}
	}
	return true
}

func (a *agentRun) learningRoot() string {
	if a.input.Role == "ideate" {
		return a.project.Path
	}
	return a.input.CWD
}
