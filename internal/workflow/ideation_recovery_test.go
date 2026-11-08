package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

// Large frozen evidence must reach generation without changing the context limit.
func TestIdeationLargeSnapshotReachesProvider(t *testing.T) {
	openshell.Install(t)
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	p, _ := store.Decode[model.Provider](record)
	p.Capabilities = append(p.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", p, record.Version); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "ideate", model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	project, _ := e.Project(ctx, task.ProjectID)
	project.Policy.ContextInputTokens = 8192
	index, err := e.index(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	input := "Preserve user constraint: never publish.\n" + strings.Repeat("captured evidence 界\n", 4000) + "\nEND OF FROZEN EVIDENCE"
	checker := &snapshotReader{t: t, full: input, limit: 8192}
	e.Adapter = func(model.Provider) provider.Adapter { return checker }
	_, err = e.generateDirections(ctx, project, input, nil, discoverySnapshot{Commit: index.Commit})
	if err != nil {
		t.Fatalf("large ideation input failed: %v", err)
	}
	if !checker.called {
		t.Fatal("generation never reached provider")
	}
}

type snapshotReader struct {
	t      *testing.T
	full   string
	limit  int
	called bool
}

func (r *snapshotReader) Run(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
	r.called = true
	if len(in.Prompt) > r.limit {
		r.t.Fatalf("prompt exceeds allowance: %d", len(in.Prompt))
	}
	files, err := filepath.Glob(filepath.Join(in.CWD, "ideation-input-*.md"))
	if err != nil || len(files) != 1 {
		r.t.Fatalf("missing frozen input: %v %v", files, err)
	}
	client := provider.NewClient(in, provider.Hooks{})
	raw, _ := json.Marshal(map[string]any{"path": files[0]})
	result, err := client.Handle(ctx, provider.Message{Method: "fs/read_text_file", Params: raw})
	if err != nil {
		r.t.Fatal(err)
	}
	content := result.(map[string]any)["content"].(string)
	if !strings.Contains(content, r.full) || !strings.Contains(in.Prompt, filepath.Base(files[0])) {
		r.t.Fatal("frozen evidence or prompt reference lost")
	}
	return provider.Output{Text: "[]"}, nil
}

// A successful rerun must retire old project errors without clearing another project.
func TestIdeationRecoveryResolvesOnlyItsOwnErrors(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	for _, msg := range []string{"" + f.project.Name + ": git: Connection reset by peer", "other: git: Connection reset by peer"} {
		if err := f.engine.Store.Notify(ctx, "", "ideation", msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.engine.Ideate(ctx, f.project, "2026-10-29"); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.engine.Store.List(ctx, "inbox")
	for _, row := range rows {
		n, _ := store.Decode[model.Notification](row)
		if n.Kind != "ideation" {
			continue
		}
		want := strings.HasPrefix(n.Message, f.project.Name+": ")
		if n.Resolved != want {
			t.Fatalf("wrong recovery state: %+v", n)
		}
	}
}

type disconnectedGitHub struct{ fakeGitHub }

func (disconnectedGitHub) Sources(context.Context, string) (any, error) {
	return nil, fmt.Errorf("gh: read: operation timed out")
}
func (disconnectedGitHub) Issues(context.Context, string) ([]model.Issue, error) {
	return nil, fmt.Errorf("gh: read: operation timed out")
}

// Outages remain diagnosable but do not flood Inbox with raw transport messages.
func TestScheduledIdeationUsesOneRecoverableConnectionNotice(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.GitHub = disconnectedGitHub{}
	if err := e.runDailyProject(ctx, p, "2026-10-29"); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.Store.List(ctx, "inbox")
	for _, row := range rows {
		n, _ := store.Decode[model.Notification](row)
		if n.Kind == "ideation" && strings.Contains(n.Message, "operation timed out") {
			t.Fatal("raw connection error exposed")
		}
	}
	r, err := e.Store.Get(ctx, "schedules", p.ID+":2026-10-29")
	if err != nil || !strings.Contains(string(r.Data), "operation timed out") {
		t.Fatal("retry diagnostics lost")
	}
}

func TestGitHubReconciliationClearsRecoveredConnectionNotice(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.GitHub = disconnectedGitHub{}
	e.reconcileProject(ctx, p)
	e.GitHub = fakeGitHub{}
	e.reconcileProject(ctx, p)
	rows, _ := e.Store.List(ctx, "inbox")
	found := false
	for _, row := range rows {
		n, _ := store.Decode[model.Notification](row)
		if n.Kind == "github" {
			found = true
			if !n.Resolved {
				t.Fatal("recovered GitHub error still active")
			}
		}
	}
	if !found {
		t.Fatal("outage not recorded")
	}
}

// Provider deadline failures must remain distinguishable from GitHub outages.
func TestIdeationDeadlineIsNotReportedAsGitHubOutage(t *testing.T) {
	p := model.Project{Name: "basera"}
	got := connectionFailureMessage(p, context.DeadlineExceeded)
	if strings.Contains(got, "GitHub connection interrupted") {
		t.Fatalf("provider timeout mislabeled: %s", got)
	}
}
