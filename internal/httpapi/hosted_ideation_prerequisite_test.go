package httpapi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/projectregistry"
	"whirlwind/internal/store"
)

func TestIdeationChecksSelectedGitHubAccessBeforeQueueing(t *testing.T) {
	for _, tc := range []struct {
		name, githubCLI, wantError string
		hosted, agentic            bool
	}{
		{name: "local checkout does not need gh"},
		{name: "agentic hosted uses scoped connections", hosted: true, agentic: true},
		{name: "legacy hosted without gh", hosted: true, wantError: "require the gh executable"},
		{name: "legacy hosted without read access", hosted: true, githubCLI: "exit 1", wantError: "require read access to https://github.com/review-fixture/hosted-project"},
		{name: "legacy hosted with active read access", hosted: true, githubCLI: "printf '%s\n' '[{\"number\":1}]'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository, tools := t.TempDir(), t.TempDir()
			mustCommand(t, repository, "git", "init", "--quiet")
			mustCommand(t, repository, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "Seed local repository")
			if tc.hosted {
				mustCommand(t, repository, "git", "remote", "add", "origin", "https://github.com/review-fixture/hosted-project.git")
			}
			project, err := projectregistry.Load(t.Context(), "Ideation fixture", repository)
			requireAPISuccess(t, err)
			project.Status = "approved"
			server := setup(t)
			requireAPISuccess(t, server.Store.Put(t.Context(), "projects", project.ID, project, 0))
			if tc.agentic {
				requireAPISuccess(t, server.Store.Put(t.Context(), "workflow_config", "global", model.WorkflowConfig{ActiveMode: model.AgenticMode}, 0))
			}
			git, err := exec.LookPath("git")
			requireAPISuccess(t, err)
			requireAPISuccess(t, os.Symlink(git, filepath.Join(tools, "git")))
			if tc.githubCLI != "" {
				body := "#!/bin/sh\n[ \"$*\" = 'issue list --repo https://github.com/review-fixture/hosted-project --limit 1 --json number' ] || exit 99\n" + tc.githubCLI + "\n"
				requireAPISuccess(t, os.WriteFile(filepath.Join(tools, "gh"), []byte(body), 0700))
			}
			t.Setenv("PATH", tools)
			command := LocalCommand{Operation: "ideation.start", ProjectID: project.ID, ExpectedVersion: 1, IdempotencyKey: "ideation", Payload: json.RawMessage(`{"fresh":true,"goal":"Explain exports"}`)}
			_, startErr := server.ExecuteLocal(t.Context(), command)
			operations, err := server.Store.List(t.Context(), "operations")
			requireAPISuccess(t, err)
			if tc.wantError != "" {
				if startErr == nil || !strings.Contains(startErr.Error(), tc.wantError) || len(operations) != 0 {
					t.Fatalf("expected prerequisite %q before any queue write: %v, operations=%d", tc.wantError, startErr, len(operations))
				}
				if _, err := server.Store.Get(t.Context(), "ideation_briefs", project.ID); err != store.ErrNotFound {
					t.Fatalf("rejected request persisted a brief: %v", err)
				}
				return
			}
			requireAPISuccess(t, startErr)
			if len(operations) != 1 {
				t.Fatalf("expected one durable run, got %d", len(operations))
			}
			op, err := store.Decode[model.Operation](operations[0])
			requireAPISuccess(t, err)
			if op.Kind != "ideation" || op.Stage != "queued" || op.Subject != project.ID {
				t.Fatalf("incorrect durable request: %+v", op)
			}
		})
	}
}
