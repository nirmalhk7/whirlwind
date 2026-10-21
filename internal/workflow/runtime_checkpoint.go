package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/store"
)

func (a *agentRun) runtimeCheckpoint(reason string) map[string]any {
	objective := a.task.Issue.Title
	if a.task.Plan != nil {
		objective = a.task.Plan.Objective
	}
	return map[string]any{"version": 1, "task": a.task.ID, "role": a.input.Role, "plan_version": a.task.PlanVersion, "worktree": a.input.CWD, "branch": a.task.Handoff.Branch, "context_commit": a.index.Commit, "objective": string(store.Prefix([]byte(objective), 2048)), "reason": reason, "last_attempt_id": a.previousAttempt, "failing_checks": checkpointFailures(a.task.Handoff.FailingTests), "next_action": "Preserve existing work, inspect referenced evidence, and choose a different approach; do not repeat the failed action unchanged"}
}

func checkpointFailures(failures []string) []string {
	result := []string{}
	for _, failure := range failures[:min(len(failures), 4)] {
		result = append(result, string(store.Prefix([]byte(failure), 512)))
	}
	return result
}

func (a *agentAttempt) progressFingerprint() string {
	if a.input.Role == "ideate" {
		return a.index.Commit
	}
	parts, err := a.trackedProgress()
	if err != nil {
		return ""
	}
	untracked, err := a.untrackedProgress()
	if err != nil {
		return ""
	}
	parts = append(parts, untracked)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func (a *agentAttempt) trackedProgress() ([]string, error) {
	commands := [][]string{{"status", "--porcelain", "-uall"}, {"diff", "HEAD"}, {"rev-parse", "HEAD"}}
	parts := make([]string, 0, len(commands))
	for _, args := range commands {
		output, err := github.Run(a.ctx, a.input.CWD, "git", args...)
		if err != nil {
			return nil, err
		}
		parts = append(parts, output)
	}
	return parts, nil
}

func (a *agentRun) attachRuntimeCheckpoint(id string) error {
	if id == "" {
		return nil
	}
	record, err := a.Store.Get(a.ctx, "runtime_checkpoints", id)
	if err != nil {
		return err
	}
	a.appendRuntimeCheckpoint(string(record.Data))
	return nil
}

func (a *agentRun) appendRuntimeCheckpoint(checkpoint string) {
	block := "\nRecovery checkpoint:\n" + checkpoint
	if !strings.Contains(a.input.Prompt, block) {
		a.input.Prompt += block
	}
}
