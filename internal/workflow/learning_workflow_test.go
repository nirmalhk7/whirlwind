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

// Envelope handling must preserve real role contracts through verified publication.
func TestLearningEnvelopesPreserveIdeationAndIssueToPR(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", source.ID, source, record.Version); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			var result any
			if in.Role == "ideate" {
				if in.ReadOnly != true {
					t.Fatal("ideation lost read-only flag")
				}
				if in.Purpose == "" && strings.Contains(in.Prompt, "Review these candidate ideas") {
					result = map[string]any{"unique": true, "reason": "distinct"}
				} else {
					result = []model.Direction{{Title: "New export", Description: "Add data export", Category: "expand"}, {Title: "Reuse index", Description: "Cache unchanged index", Category: "optimize"}, {Title: "Recover state", Description: "Recover interrupted state", Category: "harden"}, {Title: "Remove duplication", Description: "Consolidate duplicate parsers", Category: "simplify"}}
				}
			} else {
				out, err := (fakeAgent{9}).Run(ctx, in, h)
				if err != nil {
					return out, err
				}
				if json.Unmarshal([]byte(out.Text), &result) != nil {
					result = out.Text
				}
			}
			text, _ := json.Marshal(map[string]any{"result": result, "learning": map[string]any{"lessons": []model.Lesson{{Trigger: "project policy", Mistake: "assuming check commands", Correction: "Consult the committed policy", Paths: []string{".whirlwind.yaml"}}}}})
			return provider.Output{Text: string(text)}, nil
		})
	}
	if err := e.Ideate(ctx, p, "2026-10-20"); err != nil {
		t.Fatal(err)
	}
	directions, _ := e.Store.List(ctx, "directions")
	if len(directions) != 4 {
		t.Fatalf("directions=%d", len(directions))
	}
	for i := 0; i < 4; i++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "pr_created" || !done.ChecksPassed {
		t.Fatalf("workflow broken: %+v", done)
	}
	lessons, _ := e.Store.List(ctx, "lessons")
	roles := map[string]bool{}
	for _, r := range lessons {
		l, _ := store.Decode[model.Lesson](r)
		roles[l.Role] = true
	}
	if len(roles) != 4 {
		t.Fatalf("missing role memories: %+v", roles)
	}
}
