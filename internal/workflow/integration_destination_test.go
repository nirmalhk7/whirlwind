package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestNativePublicationRejectsChangedOrMultiplePushDestinations(t *testing.T) {
	for _, defect := range []string{"pushurl", "multiple", "changed-origin"} {
		t.Run(defect, func(t *testing.T) {
			e, project, task := nativePublicationFixture(t)
			remote := nativeWrongPushDestination(t, project, defect)
			calls := 0
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					text, _ := json.Marshal(map[string]string{"url": project.RemoteURL + "/pull/7", "repository": project.RemoteURL, "head": "codex/destination", "head_sha": submissionGit(t, project.Path, submissionRevParse, "HEAD"), "base": "main"})
					return provider.Output{Text: string(text), ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "open_pr", Success: true}}}, nil
				})
			}
			task.Worktree = project.Path
			step := taskStep{Engine: e, ctx: context.Background(), project: project, task: task}
			intent := model.Publication{Head: submissionGit(t, project.Path, submissionRevParse, "HEAD"), Branch: "codex/destination", Base: "main"}
			_, err := step.createIntegrationPR(intent, model.IntegrationTarget{Provider: "fake", Ref: agenticFixtureConnection}, nativePRResult{})
			if err == nil || calls != 0 {
				t.Fatalf("wrong destination reached publication: calls=%d err=%v", calls, err)
			}
			if _, err := github.Run(context.Background(), remote, "git", "show-ref", "--verify", "refs/heads/"+intent.Branch); err == nil {
				t.Fatal("reviewed code pushed to wrong repository")
			}
		})
	}
}

func nativeWrongPushDestination(t *testing.T, project model.Project, defect string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "wrong.git")
	submissionGit(t, project.Path, "init", "--bare", remote)
	submissionGit(t, project.Path, "remote", "add", "origin", project.RemoteURL)
	if defect == "changed-origin" {
		submissionGit(t, project.Path, "remote", "set-url", "origin", remote)
		return remote
	}
	submissionGit(t, project.Path, "config", "--add", "remote.origin.pushurl", remote)
	if defect == "multiple" {
		second := filepath.Join(t.TempDir(), "second.git")
		submissionGit(t, project.Path, "init", "--bare", second)
		submissionGit(t, project.Path, "config", "--add", "remote.origin.pushurl", second)
	}
	return remote
}
