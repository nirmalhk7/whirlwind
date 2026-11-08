package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (e *Engine) generateAndSaveIdeation(ctx context.Context, p model.Project, day string) error {
	prompt, previous, snapshot, unchanged, err := e.prepareDiscovery(ctx, p)
	if err != nil {
		return err
	}
	if unchanged {
		return e.pauseUnchangedIdeation(ctx, p, day)
	}
	directions, err := e.generateDirections(ctx, p, prompt, previous, snapshot)
	if err != nil && len(directions) == 0 {
		return e.pauseFailedIdeation(ctx, p, day, err)
	}
	for n := range directions {
		d := &directions[n]
		d.Rank = n + 1
		d.ID = store.ID()
		d.ProjectID = p.ID
		d.Day = day
	}
	saveErr := e.saveGeneratedDirections(ctx, p, day, directions, snapshot)
	if err != nil {
		return errors.Join(err, saveErr)
	}
	return saveErr
}

func (e *Engine) pauseUnchangedIdeation(ctx context.Context, project model.Project, day string) error {
	if err := e.Store.SaveDirections(ctx, project.ID, day, nil); err != nil {
		return err
	}
	err := fmt.Errorf("ideation incomplete: no new evidence-backed directions; missing categories: %s", strings.Join(directionCategories, ", "))
	notice := e.Store.Notify(ctx, "", "ideation_paused", fmt.Sprintf("%s: %v", project.Name, err))
	return errors.Join(err, notice)
}

func (e *Engine) pauseFailedIdeation(ctx context.Context, project model.Project, day string, failure error) error {
	if err := e.Store.SaveDirections(ctx, project.ID, day, nil); err != nil {
		return errors.Join(failure, err)
	}
	message := fmt.Sprintf("%s: ideation paused before a complete result; retry or update the Ideation ACP queue: %v", project.Name, failure)
	return errors.Join(failure, e.Store.Notify(ctx, "", "ideation_paused", message))
}

func (e *Engine) saveGeneratedDirections(ctx context.Context, p model.Project, day string, directions []model.Direction, snapshot discoverySnapshot) error {
	if err := e.Store.SaveDirections(ctx, p.ID, day, directions); err != nil {
		return err
	}
	if err := e.notifyGeneratedDirections(ctx, p, directions); err != nil {
		return err
	}
	if err := e.saveDiscoverySnapshot(ctx, p.ID, snapshot); err != nil {
		return err
	}
	if missing := missingDirectionCategories(directions); len(missing) > 0 {
		return fmt.Errorf("ideation incomplete: no worthwhile new direction for categories: %s", strings.Join(missing, ", "))
	}
	return validateCompleteDirections(directions)
}

func (e *Engine) notifyGeneratedDirections(ctx context.Context, project model.Project, directions []model.Direction) error {
	if len(directions) > 0 {
		message := fmt.Sprintf("%s: %d evidence-backed ideas await your selection", project.Name, len(directions))
		if missing := missingDirectionCategories(directions); len(missing) > 0 {
			message = fmt.Sprintf("%s: partial ideation saved; no worthwhile new direction for categories: %s", project.Name, strings.Join(missing, ", "))
		}
		return e.Store.Notify(ctx, "", "ideas_ready", message)
	}
	message := fmt.Sprintf("%s: ideation incomplete; no worthwhile new direction for categories: %s", project.Name, strings.Join(directionCategories, ", "))
	return e.Store.Notify(ctx, "", "ideation_paused", message)
}

// IdeateWhenAvailable queues a manual run behind active generation for this project.
func (e *Engine) generateDirections(ctx context.Context, p model.Project, prompt string, previous []model.Direction, snapshot discoverySnapshot) ([]model.Direction, error) {
	ctx = withIdeationHistoryBudget(ctx, e.settings(ctx).Ideation)
	maxAttempts := e.settings(ctx).Ideation.NoveltyRepairAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	request := newDirectionGenerationRequest(ctx, p, prompt, previous, snapshot)
	return e.runDirectionRepairs(ctx, request, maxAttempts)
}

func newDirectionGenerationRequest(ctx context.Context, project model.Project, prompt string, previous []model.Direction, snapshot discoverySnapshot) directionGenerationRequest {
	snapshotID := snapshot.Fingerprint
	if snapshotID == "" {
		snapshotID = model.Fingerprint([]byte(prompt))
	}
	budgetTaskID := ideationPrefix + project.ID + ":" + snapshotID[:12]
	if operation := operationID(ctx); operation != "" {
		budgetTaskID = ideationPrefix + project.ID + ":" + operation
	}
	return directionGenerationRequest{project: project, prompt: prompt, previous: previous, snapshot: snapshot, runID: store.ID(), budgetTaskID: budgetTaskID}
}

func (e *Engine) runDirectionRepairs(ctx context.Context, request directionGenerationRequest, maxAttempts int) ([]model.Direction, error) {
	accepted := []model.Direction{}
	var rejected error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		request.accepted = accepted
		request.attempt = attempt
		directions, nextRejection, done, err := e.runDirectionRepairAttempt(ctx, request, rejected)
		if err != nil {
			return accepted, err
		}
		if len(directions) >= len(accepted) {
			accepted = directions
		}
		if done {
			return accepted, nil
		}
		rejected = nextRejection
	}
	return accepted, nil
}

func (e *Engine) runDirectionRepairAttempt(ctx context.Context, request directionGenerationRequest, rejected error) ([]model.Direction, error, bool, error) {
	if budget := historyBudgetFrom(ctx); budget != nil {
		if err := budget.consume(request.snapshot.HistoryContextBytes + request.snapshot.PlanContextBytes); err != nil {
			return nil, rejected, false, err
		}
	}
	scratch, cleanup, err := e.ideationWorktree(ctx, request.project, request.snapshot.Commit, request.runID, "generator", request.attempt)
	if err != nil {
		return nil, rejected, false, err
	}
	defer cleanup()
	request.scratch = scratch
	return e.generateDirectionAttempt(ctx, request, rejected)
}

type directionGenerationRequest struct {
	project      model.Project
	prompt       string
	previous     []model.Direction
	snapshot     discoverySnapshot
	runID        string
	scratch      string
	budgetTaskID string
	accepted     []model.Direction
	attempt      int
}

func (e *Engine) generateDirectionAttempt(ctx context.Context, request directionGenerationRequest, rejected error) ([]model.Direction, error, bool, error) {
	input := request.prompt
	if rejected != nil {
		input += "\nRepair only these missing or duplicate categories: " + strings.Join(missingDirectionCategories(request.accepted), ", ") + ". Keep all previously accepted proposals unchanged. Prior batch finding: " + rejected.Error()
	}
	if len(request.accepted) > 0 {
		input += "\nAlready accepted proposals to preserve verbatim: " + jsonText(request.accepted) + "\nGenerate candidates only for the missing categories: " + strings.Join(missingDirectionCategories(request.accepted), ", ")
	}
	out, err := e.agent(ctx, request.project, model.Task{ID: request.budgetTaskID, Issue: model.Issue{Title: "innovation"}}, provider.Input{Role: "ideate", Purpose: "generation", CWD: request.scratch, Prompt: input, ReadOnly: true})
	if err != nil {
		return nil, nil, false, err
	}
	var directions []model.Direction
	if err := provider.JSON(out.Text, &directions); err != nil {
		return nil, err, false, nil
	}
	for n := range directions {
		directions[n].ProjectID = request.project.ID
	}
	return e.validateDirectionAttempt(ctx, request, directions)
}
