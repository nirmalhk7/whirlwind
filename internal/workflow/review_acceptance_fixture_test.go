package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/testutil/openshell"
)

func newAgenticReviewFixture(t *testing.T, failingCheck bool) *agenticWorkflowFixture {
	t.Helper()
	fixture := openshell.Install(t)
	home, cache := t.TempDir(), t.TempDir()
	fixture.EnableGoTestPayloadExecution(home, cache)
	moduleCache := filepath.Join(home, "go", "pkg", "mod")
	t.Cleanup(func() {
		if err := restoreAgenticGoModuleCachePermissions(moduleCache); err != nil {
			t.Errorf("restore permissions for fixture-owned Go module cache: %v", err)
		}
	})
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, repo := testEngine(t)
	workflow := &agenticWorkflowFixture{t: t, ctx: context.Background(), e: e, task: task, repo: repo}
	workflow.prepareRepository()
	if failingCheck {
		addAgenticFailingCheck(t, repo)
	}
	workflow.configureNativeProvider()
	workflow.approveLocalPlan()
	return workflow
}

func addAgenticFailingCheck(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, ".whirlwind.yaml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(config), "allowed_paths:", "  - name: failing\n    command: [false]\nallowed_paths:", 1)
	if updated == string(config) {
		t.Fatal("fixture could not add failing configured check")
	}
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "add", ".whirlwind.yaml")
	submissionGit(t, repo, "commit", "-m", "add required failing check")
}
