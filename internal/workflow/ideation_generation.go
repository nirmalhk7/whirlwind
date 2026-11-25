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
		if errors.Is(err, errIdeationHistoryBudget) {
			return e.pauseFailedIdeation(ctx, p, day, err)
		}
		return err
	}
	if err := e.validateIdeationRunIdentity(ctx, p, snapshot.Commit); err != nil {
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
		if d.ID == "" {
			d.ID = model.NewIdeaID()
		}
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
	if err := e.Store.SaveDirectionsForSlot(ctx, project.ID, day, ideationScheduleSlot(ctx, day), nil); err != nil {
		return err
	}
	err := fmt.Errorf("ideation incomplete: no new evidence-backed directions; missing categories: %s", strings.Join(directionCategories, ", "))
	notice := e.Store.Notify(ctx, "", "ideation_paused", fmt.Sprintf("%s: %v", project.Name, err))
	return errors.Join(err, notice)
}

func (e *Engine) pauseFailedIdeation(ctx context.Context, project model.Project, day string, failure error) error {
	if err := e.Store.SaveDirectionsForSlot(ctx, project.ID, day, ideationScheduleSlot(ctx, day), nil); err != nil {
		return errors.Join(failure, err)
	}
	message := fmt.Sprintf("%s: ideation paused before a complete result; retry or update the Ideation ACP queue: %v", project.Name, failure)
	return errors.Join(failure, e.Store.Notify(ctx, "", "ideation_paused", message))
}

func (e *Engine) saveGeneratedDirections(ctx context.Context, p model.Project, day string, directions []model.Direction, snapshot discoverySnapshot) error {
	if err := e.Store.SaveDirectionsForSlot(ctx, p.ID, day, ideationScheduleSlot(ctx, day), directions); err != nil {
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
	var err error
	ctx, err = e.withDurableIdeationHistoryBudget(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	maxAttempts := e.settings(ctx).Ideation.NoveltyRepairAttempts
	if operationID(ctx) != "" {
		maxAttempts = e.settings(ctx).Ideation.MaxAttemptsPerDirection
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
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
	runID := operationID(ctx)
	if runID == "" {
		runID = store.ID()
	}
	return directionGenerationRequest{project: project, prompt: prompt, previous: previous, snapshot: snapshot, runID: runID, budgetTaskID: budgetTaskID}
}

func (e *Engine) runDirectionRepairs(ctx context.Context, request directionGenerationRequest, maxAttempts int) ([]model.Direction, error) {
	accepted, err := e.acceptedDirectionRepairs(ctx, request)
	if err != nil {
		return nil, err
	}
	return e.repairDirections(ctx, request, accepted, maxAttempts)
}

func (e *Engine) acceptedDirectionRepairs(ctx context.Context, request directionGenerationRequest) ([]model.Direction, error) {
	if operationID(ctx) == "" {
		return []model.Direction{}, nil
	}
	return e.acceptedRunDirections(ctx, request.runID)
}

func (e *Engine) repairDirections(ctx context.Context, request directionGenerationRequest, accepted []model.Direction, maxAttempts int) ([]model.Direction, error) {
	var rejected error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if len(missingDirectionCategories(accepted)) == 0 {
			return accepted, nil
		}
		request.accepted = accepted
		request.attempt = attempt
		if err := e.reserveRepairCandidates(ctx, &request); err != nil {
			if exhaustedCandidateAllowance(err) {
				return accepted, nil
			}
			return accepted, err
		}
		directions, nextRejection, done, err := e.runDirectionRepairAttempt(ctx, request, rejected)
		if err != nil {
			return accepted, err
		}
		if err := e.publishDirectionCandidates(ctx, request, directions); err != nil {
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

func (e *Engine) reserveRepairCandidates(ctx context.Context, request *directionGenerationRequest) error {
	if operationID(ctx) == "" {
		return nil
	}
	var err error
	request.candidateIDs, err = e.reserveDirectionCandidates(ctx, request.runID, request.accepted)
	return err
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
	candidateIDs map[string]string
}

func (e *Engine) generateDirectionAttempt(ctx context.Context, request directionGenerationRequest, rejected error) ([]model.Direction, error, bool, error) {
	recovered, pending, err := e.loadReservedDirectionCandidates(ctx, request.candidateIDs)
	if err != nil {
		return nil, nil, false, err
	}
	durable := operationID(ctx) != ""
	if durable && len(pending) == 0 {
		return e.validateDirectionAttempt(ctx, request, recovered)
	}
	input := request.generationPrompt(durable, pending, rejected)
	out, err := e.agent(ctx, request.project, model.ExecutionPlan{ID: request.budgetTaskID, Issue: model.Issue{Title: "innovation"}}, provider.Input{Role: "ideate", Purpose: "generation", CWD: request.scratch, Prompt: input, ReadOnly: true})
	if err != nil {
		return nil, nil, false, err
	}
	directions, err := decodeGeneratedIdeas(out.Text)
	if err != nil {
		if len(recovered) > 0 {
			return e.validateDirectionAttempt(ctx, request, recovered)
		}
		return nil, err, false, nil
	}
	if durable {
		directions = reservedDirectionsOnly(directions, pending)
	}
	request.bindGeneratedDirections(directions)
	if durable {
		generated := request.generatedCandidateRecords(directions, pending)
		if err := e.Store.RecordGeneratedIdeaCandidates(ctx, generated); err != nil {
			return nil, nil, false, err
		}
	}
	return e.validateDirectionAttempt(ctx, request, append(recovered, directions...))
}

func (e *Engine) loadReservedDirectionCandidates(ctx context.Context, candidateIDs map[string]string) ([]model.Direction, map[string]string, error) {
	var recovered []model.Direction
	pending := map[string]string{}
	for category, id := range candidateIDs {
		candidate, generated, err := e.Store.GeneratedIdeaCandidate(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if generated {
			recovered = append(recovered, candidate)
		} else {
			pending[category] = id
		}
	}
	return recovered, pending, nil
}

func (request directionGenerationRequest) generationPrompt(durable bool, pending map[string]string, rejected error) string {
	input := request.prompt
	if durable {
		var allowed []string
		for _, category := range directionCategories {
			if pending[category] != "" {
				allowed = append(allowed, category)
			}
		}
		input += "\nOnly these directions have a reserved candidate allowance: " + strings.Join(allowed, ", ") + ". Do not generate any other direction."
	}
	if rejected != nil {
		input += "\nRepair only these missing or duplicate categories: " + strings.Join(missingDirectionCategories(request.accepted), ", ") + ". Keep all previously accepted proposals unchanged. Prior batch finding: " + rejected.Error()
	}
	if len(request.accepted) > 0 {
		input += "\nAlready accepted proposals to preserve verbatim: " + jsonText(request.accepted) + "\nGenerate candidates only for the missing categories: " + strings.Join(missingDirectionCategories(request.accepted), ", ")
	}
	return input
}

func reservedDirectionsOnly(directions []model.Direction, pending map[string]string) []model.Direction {
	eligible := directions[:0]
	for _, direction := range directions {
		if pending[direction.Category] != "" {
			eligible = append(eligible, direction)
		}
	}
	return eligible
}

func (request directionGenerationRequest) bindGeneratedDirections(directions []model.Direction) {
	for n := range directions {
		directions[n].ProjectID = request.project.ID
		if id := request.candidateIDs[directions[n].Category]; id != "" {
			directions[n].ID = id
		}
	}
}

func (request directionGenerationRequest) generatedCandidateRecords(directions []model.Direction, pending map[string]string) map[string]model.Direction {
	generated := map[string]model.Direction{}
	for _, direction := range directions {
		id := request.candidateIDs[direction.Category]
		if _, exists := generated[id]; id != "" && !exists {
			generated[id] = direction
		}
	}
	for category, id := range pending {
		if _, exists := generated[id]; !exists {
			generated[id] = model.Direction{ID: id, ProjectID: request.project.ID, Category: category}
		}
	}
	return generated
}
