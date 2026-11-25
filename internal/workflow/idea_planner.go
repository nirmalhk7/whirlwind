package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Intake is reconstructed from all historical runs. The deterministic resource
// name and conditional creation prevent duplicate plans after polling/restarts.
func (e *Engine) intakeApprovedIdeas(ctx context.Context) {
	records, err := e.Store.List(ctx, "ideas")
	if err != nil {
		return
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedOrder < records[j].CreatedOrder })
	for _, record := range records {
		idea, err := store.Decode[model.Idea](record)
		if err != nil || idea.DecisionStatus != "approved" || idea.ArchivedAt != "" || idea.PlanID != "" {
			continue
		}
		project, err := e.Project(ctx, idea.ProjectID)
		if err != nil || project.Paused {
			continue
		}
		if err := e.intakeApprovedIdea(ctx, record, idea, project); err != nil {
			return
		}
	}
}

func (step *taskStep) generateIdeaPlan() error {
	content, err := step.prepareIdeaPlanPublication()
	if err != nil {
		return err
	}
	return step.commitIdeaPlanPublication(content)
}

func (step *taskStep) commitIdeaPlanPublication(content string) error {
	return step.Store.CommitPlannerPublication(step.ctx, step.task, step.record.Version, func() error {
		_, err := step.writeLocalPlanFile(step.task, content)
		if err != nil {
			return fmt.Errorf("could not publish plan.md; check the local plans directory and retry planning: %w", err)
		}
		return nil
	})
}

func (step *taskStep) prepareIdeaPlanPublication() (string, error) {
	idea, err := step.planningIdea()
	if err != nil {
		return "", err
	}
	workspace, err := step.prepareIdeaPlannerWorkspace()
	if err != nil {
		return "", err
	}
	// Successful ACP output survives a later file/database failure. Reconcile it
	// before launching any replacement attempt.
	attemptID, err := step.completedIdeaPlannerAttempt()
	if err != nil {
		return "", err
	}
	if attemptID == "" {
		input, cleanup, err := step.ideaPlannerInput(idea, workspace)
		if err != nil {
			return "", err
		}
		if cleanup != nil {
			defer cleanup()
		}

		out, err := step.agent(step.ctx, step.project, step.task, input)
		if err != nil {
			return "", err
		}
		attemptID = out.AttemptID
	}
	content, err := step.recoverIdeaPlannerMarkdown(workspace.publication, workspace.output)
	if err != nil {
		return "", err
	}
	plan, err := step.parseIdeaPlannerDocument(content)
	if err != nil {
		return "", fmt.Errorf("the assigned plan.md is not publishable; correct the reported problem and retry planning: %w", err)
	}
	step.task.Plan, step.task.LastAttemptID = &plan, attemptID
	if err := step.Store.PlannerRunLocation(step.ctx, attemptID, workspace.publication); err != nil {
		return "", err
	}
	step.task.PlanDocument.Path, step.task.PlanDocument.Hash = workspace.publication, localPlanHash(content)
	step.task.Stage, step.task.Error = "plan_approval", ""
	step.task.Handoff.NextAction = "Planner completed. Review and separately approve the saved specification."
	return content, nil
}

func (step *taskStep) completedIdeaPlannerAttempt() (string, error) {
	var id string
	// Legacy supervisors stored "complete" when the provider exited. Recognize
	// both shapes so an upgrade can reconcile an already-written document.
	err := step.Store.DB.QueryRowContext(step.ctx, `
		SELECT p.planner_run_id FROM planner_runs p
		JOIN resources s ON s.kind='sessions' AND s.id=p.planner_run_id
		WHERE p.idea_id=? AND p.status='in_process'
		AND json_extract(s.data,'$.plan_id')=?
		AND json_extract(s.data,'$.plan_version')=?
		AND json_extract(s.data,'$.finished')=1
		AND ((json_extract(s.data,'$.status')=? AND json_extract(s.data,'$.success') IS NULL)
		  OR (json_extract(s.data,'$.status')='complete' AND json_extract(s.data,'$.success')=1))
		ORDER BY p.timestamp DESC LIMIT 1`, step.task.IdeaID, step.task.ID, step.task.PlanVersion, model.AttemptAwaitingPlanPublication).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (step *taskStep) recoverIdeaPlannerMarkdown(publication, output string) (string, error) {
	if _, err := os.Lstat(publication); err == nil {
		probe := step.task
		metadata := *probe.PlanDocument
		metadata.Path = publication
		probe.PlanDocument = &metadata
		return step.readLocalPlanFile(probe)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	root, err := localPlanRoot(output)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	content, err := readBoundedLocalPlan(root, "plan.md")
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("the Planner did not write the assigned plan.md; check the attempt log for output write errors and retry planning: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("could not read the assigned plan.md; check the output file and retry planning: %w", err)
	}
	return content, nil
}

func (e *Engine) intakeApprovedIdea(ctx context.Context, record model.Record, idea model.Idea, project model.Project) error {
	id := model.PlanID("idea:" + idea.ID)
	if current, err := e.Store.Get(ctx, "execution_plans", id); err == nil {
		existing, err := store.Decode[model.ExecutionPlan](current)
		if err != nil || existing.IdeaID != idea.ID || existing.IdeationRunID != idea.RunID || existing.Selection == nil || existing.Selection.CandidateVersion >= record.Version || (existing.Stage != "paused" && existing.Stage != "cancelled") {
			return nil
		}
		existing.PlanVersion++
		existing.Stage, existing.ResumeStage, existing.Error = "planning", "", ""
		existing.Plan, existing.PlanDocument, existing.LastAttemptID = nil, nil, ""
		authorization := *existing.Selection
		authorization.CandidateVersion, authorization.SelectedAt = record.Version, time.Now().UTC().Format(time.RFC3339Nano)
		existing.Selection = &authorization
		if err := e.saveApprovedIdeaIntake(ctx, record, existing, current.Version); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil
	}
	plan := model.ExecutionPlan{ID: id, ProjectID: idea.ProjectID, IdeaID: idea.ID, IdeationRunID: idea.RunID, Mode: model.AgenticMode, Stage: "planning", PlanVersion: 1, Issue: model.Issue{Title: idea.What, Body: jsonText(idea)}, Selection: &model.SelectionAuthorization{CandidateID: idea.ID, CandidateVersion: record.Version, Objective: idea.What, Scope: []string{idea.Where}, PolicyHash: project.PolicyHash, SourceFingerprint: idea.Fingerprint, SelectedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if err := e.saveApprovedIdeaIntake(ctx, record, plan, 0); err != nil && !errors.Is(err, store.ErrConflict) {
		return err
	}
	return nil
}

func (e *Engine) saveApprovedIdeaIntake(ctx context.Context, idea model.Record, plan model.ExecutionPlan, version int) error {
	return e.Store.Write(ctx, []store.Change{
		store.VersionPrecondition("ideas", idea.ID, idea.Version),
		{Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: version},
	}, nil)
}

func (step *taskStep) planningIdea() (model.Idea, error) {
	record, err := step.Store.Get(step.ctx, "ideas", step.task.IdeaID)
	if err != nil {
		return model.Idea{}, err
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return model.Idea{}, err
	}
	if idea.ProjectID != step.project.ID || idea.RunID != step.task.IdeationRunID || idea.ArchivedAt != "" || (idea.DecisionStatus != "approved" && idea.DecisionStatus != "in_process") {
		return model.Idea{}, store.ErrConflict
	}
	return idea, nil
}

type ideaPlannerWorkspace struct{ worktree, publication, output string }

func (step *taskStep) prepareIdeaPlannerWorkspace() (ideaPlannerWorkspace, error) {
	path, err := step.planningWorktree()
	if err != nil {
		return ideaPlannerWorkspace{}, err
	}
	if err := step.restoreLocalPlanIdentity(step.ctx, step.project, &step.task); err != nil {
		return ideaPlannerWorkspace{}, err
	}
	root, relative, err := step.localPlanLocation(step.task)
	if err != nil {
		return ideaPlannerWorkspace{}, err
	}
	publication := filepath.Join(root, relative)
	output := filepath.Join(path, ".whirlwind-plan-output-"+step.task.ID)
	if err := os.MkdirAll(output, 0700); err != nil {
		return ideaPlannerWorkspace{}, err
	}
	info, err := os.Lstat(output)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ideaPlannerWorkspace{}, errors.New("assigned Planner output must be a regular directory")
	}
	return ideaPlannerWorkspace{worktree: path, publication: publication, output: output}, nil
}

func (step *taskStep) ideaPlannerInput(idea model.Idea, workspace ideaPlannerWorkspace) (provider.Input, func(), error) {
	prompt, err := step.planningPrompt()
	if err != nil {
		return provider.Input{}, nil, err
	}
	prompt += "\nApproved idea (supervisor-owned identity and complete content):\n" + jsonText(idea) + "\nWrite real Markdown in the parseable plan-document format to exactly " + filepath.Join(workspace.output, "plan.md") + ". The repository is read-only. Only the assigned output directory is writable. Include sections What, Why, Benefit, Where, Scope, Exclusions, Acceptance criteria, Implementation steps, Implementation, Requirements, Checks, Repository test commands, Repository build commands, Repository test command inventory and Repository build command inventory. Requirements, Checks and Implementation must use the existing JSON code-block document format. Return a short completion confirmation. Do not approve or execute the plan."
	input, cleanup, err := step.planningIntegrationInput(workspace.worktree, prompt)
	if err != nil {
		return provider.Input{}, nil, err
	}
	input.Purpose, input.OperationID = "idea_planning", idea.RunID
	input.PlannerOutputDirectory, input.PlannerPublicationPath = workspace.output, workspace.publication
	return input, cleanup, nil
}

func (step *taskStep) parseIdeaPlannerDocument(content string) (model.PlanSpecification, error) {
	plan, err := ParsePlanDocument(content)
	if err != nil {
		return model.PlanSpecification{}, err
	}
	if err := validateDetailedPlan(plan); err != nil {
		return model.PlanSpecification{}, err
	}
	if err := validatePlanRequirements(plan.Requirements); err != nil {
		return model.PlanSpecification{}, err
	}
	if err := ValidateUnits(plan.Units); err != nil {
		return model.PlanSpecification{}, err
	}
	if err := ValidateUnitScopes(step.project, plan.Units); err != nil {
		return model.PlanSpecification{}, err
	}
	for _, path := range plan.Where {
		if !safePlanDocumentPath(path) {
			return model.PlanSpecification{}, fmt.Errorf("invalid plan: Planner returned unsafe implementation path %q", path)
		}
	}
	if err := validatePlanCommandReferences(step.project.Policy, plan); err != nil {
		return model.PlanSpecification{}, err
	}
	return plan, nil
}
