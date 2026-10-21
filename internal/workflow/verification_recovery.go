package workflow

import (
	"context"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (e *Engine) recoverVerificationStall(ctx context.Context, task *model.Task) error {
	run := &agentRun{Engine: e, ctx: ctx, task: *task, input: provider.Input{Role: "code", CWD: task.Worktree}}
	count, _, err := run.recoveryCount()
	if err != nil {
		return err
	}
	if count >= 2 {
		task.UnchangedFailures = 0
		task.Recoveries++
		task.ResumeStage = task.Stage
		task.Stage = "paused"
		task.Error = "Automatic recovery exhausted after two restarts"
		_ = e.Store.Notify(ctx, task.ID, "stall", task.Error)
		return nil
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		return err
	}
	run.project = project
	run.previousAttempt = task.LastAttemptID
	if err := run.rotateSession("Verification failed repeatedly with unchanged diff", true); err != nil {
		return err
	}
	task.UnchangedFailures = 0
	task.Recoveries++
	task.Handoff.NextAction = "Inspect recovery checkpoint and choose a different approach"
	return nil
}
