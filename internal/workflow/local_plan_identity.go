package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Pin the document name before publishing a file. A request's later task write
// may never commit, but retries must still find the same human-edited document.
func (e *Engine) restoreLocalPlanIdentity(ctx context.Context, project model.Project, task *model.Task) error {
	ctx = store.Unstaged(ctx)
	key := fmt.Sprintf("%s:v%d", task.ID, task.PlanVersion)
	record, err := e.Store.Get(ctx, "plan_drafts", key)
	if errors.Is(err, store.ErrNotFound) {
		draft, createErr := e.newLocalPlanDraft(project, *task)
		if createErr != nil {
			return createErr
		}
		if err = e.Store.Put(ctx, "plan_drafts", key, draft, 0); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
		// Concurrent recovery may have won the initial identity write.
		record, err = e.Store.Get(ctx, "plan_drafts", key)
	}
	if err != nil {
		return err
	}
	draft, err := store.Decode[model.PlanDraft](record)
	if err != nil {
		return err
	}
	if draft.TaskID != task.ID || draft.ProjectID != task.ProjectID || draft.PlanVersion != task.PlanVersion || draft.CreatedAt == "" || draft.RepoName == "" {
		return errors.New("persisted local plan draft identity does not match this plan")
	}
	identity := model.PlanDocument{CreatedAt: draft.CreatedAt, RepoName: draft.RepoName, PlanVersion: draft.PlanVersion}
	probe := *task
	probe.PlanDocument = &identity
	root, relative, err := e.localPlanLocation(probe)
	if err != nil {
		return err
	}
	if draft.Path != filepath.Join(root, relative) {
		return errors.New("persisted local plan draft path does not match this plan")
	}
	task.PlanDocument = &identity
	return nil
}

func (e *Engine) newLocalPlanDraft(project model.Project, task model.Task) (model.PlanDraft, error) {
	repoName := safePlanRepoName(project.Name)
	if repoName == "" {
		repoName = safePlanRepoName(filepath.Base(project.Path))
	}
	if repoName == "" {
		return model.PlanDraft{}, errors.New("repository name is required for a local plan document")
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	task.PlanDocument = &model.PlanDocument{CreatedAt: created, RepoName: repoName, PlanVersion: task.PlanVersion}
	root, relative, err := e.localPlanLocation(task)
	if err != nil {
		return model.PlanDraft{}, err
	}
	return model.PlanDraft{RepoName: repoName, CreatedAt: created, Path: filepath.Join(root, relative), ProjectID: task.ProjectID, PlanVersion: task.PlanVersion, TaskID: task.ID}, nil
}
