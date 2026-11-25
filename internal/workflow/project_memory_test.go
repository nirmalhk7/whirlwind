package workflow

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestProjectMemoryIsBoundedAndScopedInAgentInput(t *testing.T) {
	ctx := t.Context()
	engine, task, _ := testEngine(t)
	project, err := engine.Project(ctx, task.ProjectID)
	requireWorkflowSuccess(t, err)
	view, err := engine.ReadProjectMemory(ctx, project.ID, "code")
	requireWorkflowSuccess(t, err)
	if view.Enabled || len(view.Lessons) != 0 {
		t.Fatal("legacy project unexpectedly has memory")
	}
	project = setTestMemoryPolicy(t, engine, project, true)
	other := project
	other.ID = "separate-memory-project"
	other = setTestMemoryPolicy(t, engine, other, true)

	remember := func(p model.Project, role string, n int, note string, version int) (int, string) {
		t.Helper()
		id := fmt.Sprintf("%s-%s-memory-%02d", p.ID, role, n)
		source := model.Attempt{AttemptID: id, Role: role, ProjectID: p.ID, Task: task.ID, PlanVersion: 4,
			Finished: true, FinishedAt: "2026-01-02T00:00:00Z", PolicyHash: "source-policy", ContextCommit: "source-commit"}
		requireWorkflowSuccess(t, engine.Store.Put(ctx, "sessions", id, source, 0))
		current, err := engine.RememberProjectLesson(ctx, p.ID, role, id, note, p.PolicyHash, version)
		requireWorkflowSuccess(t, err)
		return current, id
	}
	version := 0
	var source string
	for n := range maxRoleLessons + 2 {
		note := fmt.Sprintf("unrelated observation %02d", n)
		if n == 3 {
			note = "Fix the indexing regression by using the repository fixture. api_key=synthetic-test-token"
		}
		version, source = remember(project, "code", n, note, version)
	}
	_, otherSource := remember(other, "code", 1, "indexing regression other project", 0)
	remember(project, "review", 1, "indexing regression Reviewer only", 0)

	if _, err := engine.RememberProjectLesson(ctx, project.ID, "code", source, strings.Repeat("a", maxLessonBytes+1), project.PolicyHash, version); err == nil {
		t.Fatal("oversize note was accepted")
	}
	if _, err := engine.RememberProjectLesson(ctx, project.ID, "code", otherSource, "foreign source", project.PolicyHash, version); err == nil {
		t.Fatal("source from a different project was accepted")
	}
	if _, err := engine.RememberProjectLesson(ctx, project.ID, "plan", source, "different role", project.PolicyHash, 0); err == nil {
		t.Fatal("source from a different role was accepted")
	}
	got, err := engine.ReadProjectMemory(ctx, project.ID, "code")
	requireWorkflowSuccess(t, err)
	if len(got.Lessons) != maxRoleLessons || got.Version != version || got.Lessons[len(got.Lessons)-1].Note != "unrelated observation 02" {
		t.Fatalf("memory did not evict the oldest notes: %+v", got)
	}
	if _, err := engine.ClearProjectMemory(ctx, project.ID, "code", version-1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale clear: %v", err)
	}
	if _, err := engine.RememberProjectLesson(ctx, project.ID, "code", source, got.Lessons[0].Note, project.PolicyHash, version-1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale remember: %v", err)
	}
	if repeated, err := engine.RememberProjectLesson(ctx, project.ID, "code", source, got.Lessons[0].Note, project.PolicyHash, version); repeated != version || err != nil {
		t.Fatalf("duplicate note advanced memory: version=%d, error=%v", repeated, err)
	}

	task.Issue.Title, task.Issue.Body = "Indexing regression", ""
	run := &agentRun{Engine: engine, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", Prompt: "Current approved task"}}
	requireWorkflowSuccess(t, run.buildContextPack())
	prompt := run.input.Prompt
	for _, expected := range []string{"Current approved task", "Fix the indexing regression", "source-policy", "source-commit", "api_key=[redacted]"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("missing %q in prompt: %s", expected, prompt)
		}
	}
	for _, forbidden := range []string{"synthetic-test-token", "other project", "Reviewer only", "unrelated observation 11"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("unexpected recall of %q in prompt: %s", forbidden, prompt)
		}
	}
	if strings.Count(prompt, `"note":`) != maxRecallLessons || !strings.HasPrefix(prompt, "Current approved task") {
		t.Fatalf("incorrect bounded advisory input: %s", prompt)
	}
	mandatory := "Current approved task"
	pack, err := (ctxengine.Index{}).Pack(ctxengine.PackRequest{Required: mandatory, MaxInputTokens: 256, Advisory: strings.Repeat("x", 512)})
	requireWorkflowSuccess(t, err)
	if !strings.HasPrefix(pack.Prompt, mandatory) || strings.Contains(pack.Prompt, "xxxxx") {
		t.Fatal("advisory took priority over required prompt")
	}

	var copied int
	requireWorkflowSuccess(t, engine.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE instr(data,?) > 0", "Fix the indexing regression").Scan(&copied))
	if copied != 0 {
		t.Fatal("memory contents copied into generic events")
	}
	setTestMemoryPolicy(t, engine, project, false)
	if prompt, err := run.projectMemoryPrompt(); prompt != "" || err != nil {
		t.Fatalf("stale enabled project retained recall: %q %v", prompt, err)
	}
	if _, err := engine.RememberProjectLesson(ctx, project.ID, "code", source, "replay prior policy", project.PolicyHash, version); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("write survived opt-out: %v", err)
	}
	reapproved := setTestMemoryPolicy(t, engine, project, true)
	got, err = engine.ReadProjectMemory(ctx, reapproved.ID, "code")
	requireWorkflowSuccess(t, err)
	if !got.Enabled || len(got.Lessons) != 0 {
		t.Fatalf("earlier approval's notes returned on same-content re-approval: %+v", got)
	}
	setTestMemoryPolicy(t, engine, project, false)
	_, err = engine.ClearProjectMemory(ctx, project.ID, "code", version)
	requireWorkflowSuccess(t, err)
	remembered := setTestMemoryPolicy(t, engine, project, true)
	got, err = engine.ReadProjectMemory(ctx, remembered.ID, "code")
	requireWorkflowSuccess(t, err)
	if !got.Enabled || len(got.Lessons) != 0 {
		t.Fatalf("cleared notes returned after re-approval: %+v", got)
	}
}

func setTestMemoryPolicy(t *testing.T, engine *Engine, project model.Project, enable bool) model.Project {
	t.Helper()
	ctx := t.Context()
	content := fmt.Sprintf("project_memory: %t\nallowed_paths: [result.txt]\nrisk_rules: [{level: low, paths: ['**']}]\nchecks: [{name: regression, command: [true-check]}]\n", enable)
	p, hash, err := policy.ParseManagedPolicy(project.ID, content)
	requireWorkflowSuccess(t, err)
	project.ManagedPolicy, project.Policy, project.PolicyHash = true, p, hash
	save := func(kind string, value any) {
		t.Helper()
		r, err := engine.Store.Get(ctx, kind, project.ID)
		if !errors.Is(err, store.ErrNotFound) {
			requireWorkflowSuccess(t, err)
		}
		requireWorkflowSuccess(t, engine.Store.Put(ctx, kind, project.ID, value, r.Version))
	}
	save(policy.ManagedPolicyResource, model.ProjectPolicyDocument{ProjectID: project.ID, Content: content})
	save("projects", project)
	return project
}
