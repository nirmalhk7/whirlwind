package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestEvidenceFailureStopsQuotaFallback(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.Providers = []string{"fake", "second"}
	if err := e.Store.Put(ctx, "providers", "second", model.Provider{ID: "second", Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"plan"}}, 0); err != nil {
		t.Fatal(err)
	}
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_event BEFORE INSERT ON attempt_events WHEN NEW.kind='item/agentMessage/delta' BEGIN SELECT RAISE(ABORT,'event unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	calledSecond := false
	e.Adapter = func(p model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			if p.ID == "second" {
				calledSecond = true
				return provider.Output{Text: "success"}, nil
			}
			h.Event("item/agentMessage/delta", map[string]string{"delta": "partial"})
			return provider.Output{}, fmt.Errorf("quota exhausted")
		})
	}
	_, err = e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "request", ReadOnly: true})
	if !errors.Is(err, store.ErrPersistence) || calledSecond {
		t.Fatalf("evidence failure swallowed by fallback: %v second=%v", err, calledSecond)
	}
}

func TestPermissionEvidenceFailureCannotBeSwallowed(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.Commands = [][]string{{"safe"}}
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON resources WHEN NEW.kind='audit' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			_, _ = h.Permission(ctx, provider.Permission{Command: []string{"safe"}})
			return provider.Output{Text: "pretend success"}, nil
		})
	}
	_, err = e.agent(ctx, p, task, provider.Input{Role: "code", CWD: path, Prompt: "request"})
	if !errors.Is(err, store.ErrPersistence) {
		t.Fatal("permission persistence failure ignored", err)
	}
}

func TestUncertainPublicationReconcilesFrozenIntent(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	remote := &uncertainPublication{fakeGitHub: fakeGitHub{}}
	e.GitHub = remote
	for i := 0; i < 6; i++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Step(ctx, task.ID); err == nil {
		t.Fatal("lost remote response accepted")
	}
	records, _ := e.Store.List(ctx, "publications")
	if len(records) != 1 || !strings.Contains(string(records[0].Data), "uncertain") {
		t.Fatal("uncertain intent missing", records)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "pr_created" || remote.created != 1 {
		t.Fatal("publication recovery duplicated remote write", done.Stage, remote.created)
	}
}

type uncertainPublication struct {
	fakeGitHub
	created int
	body    string
}

func (g *uncertainPublication) CreatePR(_ context.Context, _, _, _, _, _, body string) (string, error) {
	if g.created == 0 {
		g.created = 1
		g.body = body
		return "", errors.New("connection lost after remote write")
	}
	if body != g.body {
		return "", errors.New("recovery changed publication body")
	}
	return "https://github.com/o/r/pull/1", nil
}

func TestEmptyConnectivityReplyHasFailedAttempt(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := WithOperation(context.Background(), "connectivity")
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, nil
		})
	}
	_, err := e.TestProviderChoice(ctx, model.Provider{ID: "fake", Kind: "acp"}, model.AgentChoice{})
	if err == nil {
		t.Fatal("empty reply accepted")
	}
	records, _ := e.Store.List(context.Background(), "sessions")
	state, _ := store.Decode[model.Attempt](records[0])
	if state.Success == nil || *state.Success {
		t.Fatal("failed connection attempt reported success", state)
	}
}

func TestAnsweredNativeQuestionsSurviveFreshAttempt(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	answer := model.QuestionSet{ID: "answered", TaskID: task.ID, PlanVersion: task.PlanVersion, Status: "answered", Questions: []model.Question{{ID: "choice", Text: "Use which database?"}}, Answers: map[string]string{"choice": "SQLite"}}
	if err := e.Store.Put(ctx, "questions", answer.ID, answer, 0); err != nil {
		t.Fatal(err)
	}
	seen := false
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			seen = strings.Contains(in.Prompt, "Earlier answered agent questions") && strings.Contains(in.Prompt, "Use which database?") && strings.Contains(in.Prompt, "SQLite")
			return provider.Output{Text: "done"}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "continue", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("durable native answers absent from recovered input")
	}
}
