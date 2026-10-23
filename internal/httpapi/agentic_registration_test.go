package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const agenticRegistrationRef = "mcp://forge"

func agenticRegistrationRepository(t *testing.T, remote string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(bin, "gh-called")
	t.Setenv("HOME", t.TempDir())
	for name, body := range map[string]string{"gh": "#!/bin/sh\necho called >> '" + marker + "'\nexit 97\n", "qlty": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for path, body := range map[string]string{".whirlwind.yaml": "checks:\n  - name: tests\n    command: [true]\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n", ".qlty/qlty.toml": "config_version = \"0\"\n", ".codegraph/codegraph.db": "fixture"} {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "initial"}, {"remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	return repo, marker
}

func TestAgenticRegistrationDoesNotRequireGitHubCLI(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	const remote = "https://forge.example/team/repo"
	repo, marker := agenticRegistrationRepository(t, remote)
	if err := s.Store.Put(ctx, "providers", "native", model.Provider{ID: "native", Kind: "codex", Available: true, Quota: "unknown", Command: []string{"/usr/bin/true"}, Capabilities: []string{"code", "submit"}}, 0); err != nil {
		t.Fatal(err)
	}
	settings := workflow.DefaultSettings()
	settings.Admission = model.AdmissionSettings{DiscoveryBudget: &model.BudgetPolicy{MaxAttempts: 20, MaxRuntimeSeconds: 120}, DefaultBudget: &model.BudgetPolicy{MaxAttempts: 20, MaxRuntimeSeconds: 120}, Pools: map[string]model.QuotaReserve{"*": {MinRemainingPercent: 0, MaxAgeSeconds: 60, MaxConcurrent: 4}}}
	if err := s.Store.Put(ctx, "settings", "global", settings, 0); err != nil {
		t.Fatal(err)
	}
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode, Integrations: model.IntegrationSettings{Publication: map[string]model.IntegrationTarget{remote: {Provider: "native", Ref: agenticRegistrationRef}}}}
	if err := s.Store.Put(ctx, "workflow_config", "global", config, 0); err != nil {
		t.Fatal(err)
	}
	s.Engine.Adapter = func(model.Provider) provider.Adapter {
		return workflowInventoryAdapter(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if in.Purpose != "integration:repository_metadata" || !in.ReadOnly || !in.NativeIntegrations {
				t.Fatalf("registration bypassed native read operations: %+v", in)
			}
			return provider.Output{Text: `{"repository":"https://forge.example/team/repo","default_branch":"main","lookup_pr_tool":"find","create_pr_tool":"create"}`, Integrations: []model.Integration{{Provider: "native", Ref: agenticRegistrationRef, Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "metadata", ReadOnly: true}, {Name: "find", ReadOnly: true}, {Name: "create"}}}}, ToolCalls: []provider.ToolEvidence{{Ref: agenticRegistrationRef, Tool: "metadata", Success: true}}}, nil
		})
	}
	response := workflowModeRequest(t, s, "POST", "projects", "", map[string]string{"name": "Agentic project", "path": repo})
	if response.Code != 201 {
		t.Fatalf("native registration failed: %d %s", response.Code, response.Body)
	}
	var record model.Record
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil || project.RemoteURL != remote || project.DefaultBranch != "main" {
		t.Fatalf("wrong native project: %+v %v", project, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Agentic registration invoked gh")
	}
}
