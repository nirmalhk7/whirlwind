package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Losing output normalization or role/project isolation must fail this test.
func TestLearningRecallIsolatedAcrossRolesAndProjects(t *testing.T) {
	for _, role := range []string{"ideate", "plan", "code", "review"} {
		t.Run(role, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			ctx := context.Background()
			project, _ := e.Project(ctx, task.ProjectID)
			p, _ := e.Store.Get(ctx, "providers", "fake")
			v, _ := store.Decode[model.Provider](p)
			v.Capabilities = append(v.Capabilities, "ideate")
			if err := e.Store.Put(ctx, "providers", "fake", v, p.Version); err != nil {
				t.Fatal(err)
			}
			var prompts []string
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
					prompts = append(prompts, in.Prompt)
					if len(prompts) == 1 {
						return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy changes","mistake":"guessing the project checks","correction":"Read the committed policy first","paths":[".whirlwind.yaml"]}]}}`}, nil
					}
					return provider.Output{Text: "done"}, nil
				})
			}
			out, err := e.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "policy changes"})
			if err != nil {
				t.Fatal(err)
			}
			if out.Text != "done" {
				t.Fatalf("envelope leaked into role result: %s", out.Text)
			}
			task.ID = "another-task"
			if _, err = e.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "policy changes"}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompts[1], "Read the committed policy first") {
				t.Fatalf("lesson missing from later task: %s", prompts[1])
			}
			otherRole := "review"
			if role == otherRole {
				otherRole = "plan"
			}
			if _, err = e.agent(ctx, project, task, provider.Input{Role: otherRole, CWD: cwd, Prompt: "policy changes"}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(prompts[2], "Read the committed policy first") {
				t.Fatal("lesson leaked across roles")
			}
			project.ID = "other-project"
			if _, err = e.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "policy changes"}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(prompts[3], "Read the committed policy first") {
				t.Fatal("lesson leaked across projects")
			}
			records, err := e.Store.List(ctx, "lessons")
			if err != nil || len(records) != 1 {
				t.Fatalf("lessons=%d err=%v", len(records), err)
			}
			var lesson map[string]any
			_ = json.Unmarshal(records[0].Data, &lesson)
			if lesson["status"] != "tentative" || lesson["project_id"] != "p" || lesson["role"] != role {
				t.Fatalf("scope/status: %s", records[0].Data)
			}
		})
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
