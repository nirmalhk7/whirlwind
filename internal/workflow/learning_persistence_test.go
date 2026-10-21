package workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestLearningSurvivesRestartProviderChangeAndReonboarding(t *testing.T) {
	e, task, cwd := testEngine(t)
	task.Issue.Title = "policy"
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"Read committed policy","paths":[".whirlwind.yaml"]}]}}`}, nil
		})
	}
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := e.Store.Get(ctx, "sessions", out.AttemptID)
	attempt, _ := store.Decode[model.Attempt](original)
	_, raw, err := e.Store.Artifact(ctx, attempt.OutputArtifact)
	if err != nil || !strings.Contains(string(raw), `"learning"`) {
		t.Fatal("raw provider output lost", err)
	}
	taskRecord, _ := e.Store.Get(ctx, "tasks", task.ID)
	task.Stage = "paused"
	if err = e.Store.Put(ctx, "tasks", task.ID, task, taskRecord.Version); err != nil {
		t.Fatal(err)
	}
	if err = e.Store.UnregisterProject(ctx, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.Store.Put(ctx, "projects", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(filepath.Join(e.Root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}()
	next := New(other, fakeGitHub{}, e.Root)
	r, _ := other.Get(ctx, "providers", "fake")
	v, _ := store.Decode[model.Provider](r)
	v.ID = "replacement"
	if err = other.Put(ctx, "providers", v.ID, v, 0); err != nil {
		t.Fatal(err)
	}
	if err = other.Put(ctx, "agents", "code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "replacement"}}}, 0); err != nil {
		t.Fatal(err)
	}
	var prompt string
	next.Adapter = func(p model.Provider) provider.Adapter {
		if p.ID != "replacement" {
			t.Fatalf("wrong provider %s", p.ID)
		}
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			prompt = in.Prompt
			return provider.Output{Text: "done"}, nil
		})
	}
	task.ID = "later"
	if _, err = next.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Read committed policy") {
		t.Fatal("learning lost after restart/provider change/re-onboarding")
	}
}

func TestLearningConcurrentDuplicateReportsAreIdempotent(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	hash, _ := learningFingerprint(cwd, learningPolicyFixturePath)
	a := model.Attempt{AttemptID: "concurrent", ProjectID: task.ProjectID, Task: task.ID, Role: "code"}
	if err := e.Store.Put(ctx, "sessions", a.AttemptID, a, 0); err != nil {
		t.Fatal(err)
	}
	report := model.LearningReport{Lessons: []model.Lesson{{Trigger: "policy", Mistake: "guessing", Correction: "Read policy", Paths: []string{learningPolicyFixturePath}, Anchors: map[string]string{learningPolicyFixturePath: hash}}}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() { _, err := e.Store.CommitLearning(ctx, a, report); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	lessons, _ := e.Store.List(ctx, "lessons")
	audit, _ := e.Store.List(ctx, "audit")
	if len(lessons) != 1 || len(audit) != 1 {
		t.Fatalf("duplicate effects lessons=%d audit=%d", len(lessons), len(audit))
	}
}

func TestLearningSensitiveLessonsNeverEnterMemory(t *testing.T) {
	for _, extra := range []string{`,"secret":true`, `,"isSecret":true`} {
		t.Run(extra, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			ctx := context.Background()
			p, _ := e.Project(ctx, task.ProjectID)
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					return provider.Output{Text: fmt.Sprintf(`{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"confidential lesson","paths":[".whirlwind.yaml"]%s}]}}`, extra)}, nil
				})
			}
			if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
				t.Fatal(err)
			}
			lessons, _ := e.Store.List(ctx, "lessons")
			if len(lessons) != 0 {
				t.Fatal("marked secret persisted as lesson")
			}
		})
	}
}

func TestLearningDedupPreservesCaseSensitiveCodeCorrections(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	a := model.Attempt{AttemptID: "case-sensitive", ProjectID: task.ProjectID, Task: task.ID, Role: "code"}
	if err := e.Store.Put(ctx, "sessions", a.AttemptID, a, 0); err != nil {
		t.Fatal(err)
	}
	report := model.LearningReport{Lessons: []model.Lesson{
		{Trigger: "parsing", Mistake: "Call parseURL", Correction: "Call ParseURL"},
		{Trigger: "parsing", Mistake: "Call ParseURL", Correction: "Call parseURL"},
	}}
	if _, err := e.Store.CommitLearning(ctx, a, report); err != nil {
		t.Fatal(err)
	}
	lessons, _ := e.Store.List(ctx, "lessons")
	if len(lessons) != 2 {
		t.Fatal("case-sensitive code corrections were merged")
	}
}
