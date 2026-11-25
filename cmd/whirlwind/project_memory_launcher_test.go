package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func TestProjectMemoryThroughLauncher(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_SEEDED_PTY") != "1" {
		t.Skip("set WHIRLWIND_TEST_SEEDED_PTY=1 to test the shipped launcher")
	}
	root := t.TempDir()
	repoRoot, err := filepath.Abs("../..")
	mustSeedTerminal(t, err)
	state, target, home := filepath.Join(root, "state"), filepath.Join(root, "target"), filepath.Join(root, "home")
	for _, dir := range []string{state, target, home} {
		mustSeedTerminal(t, os.Mkdir(dir, 0700))
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", target, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, value string) { t.Helper(); mustSeedTerminal(t, os.WriteFile(path, []byte(value), 0600)) }
	git("init", "--template=", "-b", "fixture")
	write(filepath.Join(target, "README.md"), "Ordinary project fixture\n")
	git("add", "README.md")
	git("commit", "--no-gpg-sign", "-m", "Initialize fixture")
	commit := git("rev-parse", "HEAD")
	settings := filepath.Join(root, "settings.yaml")
	raw, err := json.Marshal(map[string]any{
		"version": 1, "mode": model.AgenticMode, "projects": []string{target},
		"workers": map[string]bool{"stopped": true}, "providers": []any{}, "agents": map[string]any{},
	})
	mustSeedTerminal(t, err)
	write(settings, string(raw))
	launcher := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(repoRoot, "bin", "whirlwind"), args...)
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "WHIRLWIND_ROOT=" + repoRoot, "WHIRLWIND_DATA_DIR=" + state, "WHIRLWIND_SETTINGS_FILE=" + settings}
		for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOFLAGS"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			log, _ := os.ReadFile(filepath.Join(state, "whirlwind-supervisor.log"))
			t.Fatalf("launcher %v: %v: %s\n%s", args, err, out, log)
		}
		return string(out)
	}
	launcher("start")
	t.Cleanup(func() { launcher("stop") })
	db := openSeededTerminalStore(t, state)
	ctx := t.Context()
	projects, err := db.List(ctx, "projects")
	mustSeedTerminal(t, err)
	if len(projects) != 1 {
		t.Fatalf("wanted one registered project, got %v", projects)
	}
	id := projects[0].ID
	list := func(role string) workflow.ProjectMemoryView {
		t.Helper()
		var view workflow.ProjectMemoryView
		mustSeedTerminal(t, json.Unmarshal([]byte(launcher("memory", "list", id, role)), &view))
		return view
	}
	if view := list("code"); view.Enabled || len(view.Lessons) != 0 {
		t.Fatalf("expected memory to be disabled before approval: %+v", view)
	}
	policyPath := filepath.Join(root, "reviewed-policy.yaml")
	write(policyPath, "project_memory: true\nallowed_paths: [README.md]\nrisk_rules: [{level: low, paths: ['**']}]\nchecks: [{name: baseline, command: ['true']}]\n")
	preview := launcher("policy", "preview", id, policyPath)
	fields := regexp.MustCompile(`Version (\d+)\nSHA256 ([a-f0-9]{64})`).FindStringSubmatch(preview)
	if len(fields) != 3 {
		t.Fatalf("read policy preview: %s", preview)
	}
	launcher("policy", "approve", id, policyPath, fields[1], fields[2])
	const planID, attemptID = "fixture-completed-plan", "fixture-completed-code"
	mustSeedTerminal(t, db.Put(ctx, "execution_plans", planID, model.ExecutionPlan{ID: planID, ProjectID: id, PlanVersion: 4, Stage: "complete"}, 0))
	source := model.Attempt{AttemptID: attemptID, ProjectID: id, Task: planID, PlanVersion: 4, Role: "code", Finished: true,
		Status: "complete", FinishedAt: time.Now().UTC().Format(time.RFC3339Nano), PolicyHash: fields[2], ContextCommit: commit}
	mustSeedTerminal(t, db.Put(ctx, "sessions", attemptID, source, 0))
	note := "Keep the generated fixture in a temporary directory."
	launcher("memory", "remember", id, "code", attemptID, note)
	before := list("code")
	if !before.Enabled || before.Version != 1 || len(before.Lessons) != 1 || before.Lessons[0].Note != note ||
		before.Lessons[0].PlanID != planID || before.Lessons[0].PlanVersion != 4 || before.Lessons[0].SourcePolicy != fields[2] || before.Lessons[0].SourceCommit != commit {
		t.Fatalf("missing lesson or source provenance: %+v", before)
	}
	if view := list("plan"); len(view.Lessons) != 0 {
		t.Fatalf("Coding note appeared in Planner memory: %+v", view)
	}
	launcher("restart")
	after := list("code")
	if !after.Enabled || after.Version != before.Version || len(after.Lessons) != 1 || after.Lessons[0] != before.Lessons[0] {
		t.Fatalf("lesson changed after supervisor restart: %+v", after)
	}
	launcher("memory", "clear", id, "code")
	if view := list("code"); len(view.Lessons) != 0 || view.Version != after.Version+1 {
		t.Fatalf("clear did not remove the recalled note: %+v", view)
	}
	if git("status", "--porcelain") != "" || git("rev-parse", "HEAD") != commit {
		t.Fatal("supervisor changed the original checkout")
	}
	t.Log("shipped launcher preserved bounded, opted-in Coding memory and provenance across restart")
}
