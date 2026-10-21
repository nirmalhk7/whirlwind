package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

const recalledPolicyCorrection = "Read the committed policy first"

// Losing output normalization or role/project isolation must fail this test.
func TestLearningRecallIsolatedAcrossRolesAndProjects(t *testing.T) {
	for _, role := range []string{"ideate", "plan", "code", "review"} {
		t.Run(role, func(t *testing.T) { assertRoleLearningIsolation(t, role) })
	}
}

func assertRoleLearningIsolation(t *testing.T, role string) {
	t.Helper()
	e, task, cwd := testEngine(t)
	task.Issue.Title = "policy changes"
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	enableLearningIdeation(t, e)
	var prompts []string
	e.Adapter = func(model.Provider) provider.Adapter { return learningRecorder(&prompts) }
	run := func(project model.Project, role string) provider.Output {
		t.Helper()
		out, err := e.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "policy changes"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := run(project, role); out.Text != "done" {
		t.Fatalf("envelope leaked into role result: %s", out.Text)
	}
	task.ID = "another-task"
	run(project, role)
	if !strings.Contains(prompts[1], recalledPolicyCorrection) {
		t.Fatalf("lesson missing from later task: %s", prompts[1])
	}
	otherRole := "review"
	if role == otherRole {
		otherRole = "plan"
	}
	run(project, otherRole)
	if strings.Contains(prompts[2], recalledPolicyCorrection) {
		t.Fatal("lesson leaked across roles")
	}
	project.ID = "other-project"
	run(project, role)
	if strings.Contains(prompts[3], recalledPolicyCorrection) {
		t.Fatal("lesson leaked across projects")
	}
	assertLearningLessonScope(t, e, role)
}

func learningRecorder(prompts *[]string) provider.Adapter {
	return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
		*prompts = append(*prompts, in.Prompt)
		if len(*prompts) == 1 {
			return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy changes","mistake":"guessing the project checks","correction":"Read the committed policy first","paths":[".whirlwind.yaml"]}]}}`}, nil
		}
		return provider.Output{Text: "done"}, nil
	})
}

func assertLearningLessonScope(t *testing.T, e *Engine, role string) {
	t.Helper()
	records, err := e.Store.List(context.Background(), "lessons")
	if err != nil || len(records) != 1 {
		t.Fatalf("lessons=%d err=%v", len(records), err)
	}
	var lesson map[string]any
	_ = json.Unmarshal(records[0].Data, &lesson)
	if lesson["status"] != "tentative" || lesson["project_id"] != "p" || lesson["role"] != role {
		t.Fatalf("scope/status: %s", records[0].Data)
	}
}

func TestLearningMalformedMetadataPreservesResult(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: `{"result":{"objective":"ok"},"learning":{"lessons":"invalid"}}`}, nil
		})
	}
	out, err := e.agent(ctx, project, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "work"})
	if err != nil || out.Text != `{"objective":"ok"}` {
		t.Fatalf("valid result lost: %s %v", out.Text, err)
	}
	records, _ := e.Store.List(ctx, "sessions")
	if len(records) != 1 || !strings.Contains(string(records[0].Data), "warnings") {
		t.Fatal("missing learning warning")
	}
}
