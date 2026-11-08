package workflow

import (
	"context"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (e *Engine) reviewDirectionNovelty(ctx context.Context, request directionGenerationRequest, directions, previous []model.Direction) (bool, string, []string, error) {
	prompt, err := agents.Render("ideate_novelty.md", map[string]string{"Candidates": boundedSources(jsonText(directions)), "Previous": boundedSources(jsonText(compactProposalHistory(previous))), "ApprovedPlans": boundedSources(jsonText(request.snapshot.ApprovedPlans)), "Repository": boundedSources(jsonText(request.snapshot.CurrentRepository)), "HumanReconsiderations": boundedSources(jsonText(request.snapshot.HumanReconsiderations))})
	if err != nil {
		return false, "", nil, err
	}
	scratch, cleanup, err := e.ideationWorktree(ctx, request.project, request.snapshot.Commit, request.runID, "novelty", request.attempt)
	if err != nil {
		return false, "", nil, err
	}
	defer cleanup()
	out, err := e.agent(ctx, request.project, model.Task{ID: request.budgetTaskID, Issue: model.Issue{Title: "idea novelty review"}}, provider.Input{Role: "ideate", Purpose: "novelty:" + store.ID(), CWD: scratch, Prompt: prompt, ReadOnly: true})
	if err != nil {
		return false, "", nil, err
	}
	return parseNoveltyVerdict(out.Text)
}

func parseNoveltyVerdict(text string) (bool, string, []string, error) {
	var verdict struct {
		Unique              *bool    `json:"unique"`
		Reason              string   `json:"reason"`
		DuplicateCategories []string `json:"duplicate_categories"`
	}
	if err := provider.JSON(text, &verdict); err != nil {
		return false, "", nil, err
	}
	if verdict.Unique == nil {
		return false, "", nil, errors.New("novelty review missing unique verdict")
	}
	unique := *verdict.Unique && len(verdict.DuplicateCategories) == 0
	return unique, verdict.Reason, verdict.DuplicateCategories, nil
}
