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
		if n >= 4 && n <= 6 {
			note = fmt.Sprintf("indexing tip %02d", n)
		}
		if n == 3 {
			note = "Fix the indexing regression by using the repository fixture. api_key=synthetic-test-token"
		}
		version, source = remember(project, "code", n, note, version)
	}
	_, otherSource := remember(other, "code", 1, "indexing regression other project", 0)
	remember(project, "review", 1, "indexing regression Reviewer only", 0)

	requireMemoryWriteBoundaries(t, engine, project, source, otherSource, version)
	run := requireMemoryPromptScope(t, engine, project, task)
	requireMemoryOptOut(t, run, source, version)
}

const (
	memoryTestPrompt = "Current approved task"
	memoryTestIssue  = "Indexing regression"
)

func TestRenderProjectMemoryOmitsUnmatchedNotes(t *testing.T) {
	const unmatchedNote = "Use small typography samples"
	notes := []model.RoleLesson{{AttemptID: "unmatched-attempt", Note: unmatchedNote}}
	recall, err := renderProjectMemory(notes, memoryTestIssue)
	requireWorkflowSuccess(t, err)
	if recall.Prompt != "" {
		t.Fatalf("unmatched lesson appeared in advisory input: %s", recall.Prompt)
	}
	if len(recall.Decisions) != 1 || recall.Decisions[0].Reason != recallUnmatched {
		t.Fatalf("missing reason for unmatched note: %+v", recall.Decisions)
	}

	notes = append(notes, model.RoleLesson{AttemptID: "matching-attempt", Note: "Use the indexing fixture"})
	recall, err = renderProjectMemory(notes, memoryTestIssue)
	requireWorkflowSuccess(t, err)
	if !strings.Contains(recall.Prompt, "Use the indexing fixture") || strings.Contains(recall.Prompt, unmatchedNote) {
		t.Fatalf("mixed recall did not isolate the relevant lesson: %s", recall.Prompt)
	}
	if len(recall.Decisions) != 2 || recall.Decisions[0] != (memoryRecallDecision{AttemptID: "matching-attempt", Reason: recallIncluded}) {
		t.Fatalf("missing reason for included note: %+v", recall.Decisions)
	}
	recall.recordContextBudget(false)
	if recall.Decisions[0].Reason != recallContextBudget {
		t.Fatalf("note rejected by the context budget reported as included: %+v", recall.Decisions)
	}
}

func TestMemoryRecallEvidenceDistinguishesOmissionsForTheSamePrompt(t *testing.T) {
	engine, task, _ := testEngine(t)
	run := &agentRun{Engine: engine, ctx: t.Context(), task: task}
	const (
		key          = "same-prompt"
		kind         = "context_packs"
		firstSource  = "source-one"
		secondSource = "source-two"
	)
	pack := ctxengine.ContextPack{ID: key, Prompt: "private advisory text"}
	for _, source := range []string{firstSource, secondSource, firstSource} {
		requireWorkflowSuccess(t, run.saveContextPack(key, pack, []memoryRecallDecision{{AttemptID: source, Reason: recallUnmatched}}))
	}
	records, err := engine.Store.List(run.ctx, kind)
	requireWorkflowSuccess(t, err)
	if len(records) != 2 {
		t.Fatalf("same prompt did not preserve distinct recall evidence: %d records", len(records))
	}
	seen := map[string]bool{}
	for _, record := range records {
		evidence, err := store.Decode[struct {
			Recall []memoryRecallDecision `json:"memory_recall"`
		}](record)
		requireWorkflowSuccess(t, err)
		if len(evidence.Recall) != 1 || evidence.Recall[0].Reason != recallUnmatched || strings.Contains(string(record.Data), pack.Prompt) {
			t.Fatal("recall evidence omitted reasons or persisted prompt prose")
		}
		seen[evidence.Recall[0].AttemptID] = true
	}
	if !seen[firstSource] || !seen[secondSource] {
		t.Fatalf("recall omitted source identities: %+v", seen)
	}
}

func requireMemoryWriteBoundaries(t *testing.T, engine *Engine, project model.Project, source, otherSource string, version int) {
	t.Helper()
	ctx := t.Context()
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
}

func requireMemoryPromptScope(t *testing.T, engine *Engine, project model.Project, task model.ExecutionPlan) *agentRun {
	t.Helper()
	ctx := t.Context()

	task.Issue.Title, task.Issue.Body = memoryTestIssue, ""
	run := &agentRun{Engine: engine, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", Prompt: memoryTestPrompt}}
	requireWorkflowSuccess(t, run.buildContextPack())
	prompt := run.input.Prompt
	for _, expected := range []string{memoryTestPrompt, "Fix the indexing regression", "source-policy", "source-commit", "api_key=[redacted]"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("missing %q in prompt: %s", expected, prompt)
		}
	}
	for _, forbidden := range []string{"synthetic-test-token", "other project", "Reviewer only", "unrelated observation 11"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("unexpected recall of %q in prompt: %s", forbidden, prompt)
		}
	}
	if strings.Count(prompt, `"note":`) != maxRecallLessons || !strings.HasPrefix(prompt, memoryTestPrompt) {
		t.Fatalf("incorrect bounded advisory input: %s", prompt)
	}
	mandatory := memoryTestPrompt
	pack, err := (ctxengine.Index{}).Pack(ctxengine.PackRequest{Required: mandatory, MaxInputTokens: 256, Advisory: strings.Repeat("x", 512)})
	requireWorkflowSuccess(t, err)
	if !strings.HasPrefix(pack.Prompt, mandatory) || strings.Contains(pack.Prompt, "xxxxx") || pack.AdvisoryIncluded {
		t.Fatal("advisory took priority over required prompt")
	}

	return run
}

func requireMemoryOptOut(t *testing.T, run *agentRun, source string, version int) {
	t.Helper()
	ctx, engine, project := t.Context(), run.Engine, run.project

	var copied int
	requireWorkflowSuccess(t, engine.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE instr(data,?) > 0", "Fix the indexing regression").Scan(&copied))
	if copied != 0 {
		t.Fatal("memory contents copied into generic events")
	}
	setTestMemoryPolicy(t, engine, project, false)
	if recall, err := run.projectMemoryPrompt(); recall.Prompt != "" || len(recall.Decisions) != 0 || err != nil {
		t.Fatalf("stale enabled project retained recall: %+v %v", recall, err)
	}
	if _, err := engine.RememberProjectLesson(ctx, project.ID, "code", source, "replay prior policy", project.PolicyHash, version); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("write survived opt-out: %v", err)
	}
	reapproved := setTestMemoryPolicy(t, engine, project, true)
	got, err := engine.ReadProjectMemory(ctx, reapproved.ID, "code")
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
