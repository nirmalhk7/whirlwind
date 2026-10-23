package workflow

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type cancellationGitHub struct {
	fakeGitHub
	issues              []model.Issue
	issueCalls, prCalls int
}

func (g *cancellationGitHub) Issues(context.Context, string) ([]model.Issue, error) {
	return g.issues, nil
}

func (g *cancellationGitHub) Issue(context.Context, string, int) (model.Issue, error) {
	g.issueCalls++
	return model.Issue{}, nil
}

func (g *cancellationGitHub) PRState(context.Context, string, string) (string, error) {
	g.prCalls++
	return "MERGED", nil
}

func TestCancelledPersonalPRCannotReconcileOrExecute(t *testing.T) {
	for _, mode := range []string{"", model.PersonalMode} {
		for _, sourcePresent := range []bool{false, true} {
			t.Run(mode+"/source-present="+strconv.FormatBool(sourcePresent), func(t *testing.T) {
				assertCancelledPersonalPR(t, mode, sourcePresent)
			})
		}
	}
}

func assertCancelledPersonalPR(t *testing.T, mode string, sourcePresent bool) {
	t.Helper()
	ctx := context.Background()
	e, task, repo := testEngine(t)
	task.Mode, task.Stage = mode, "pr_created"
	task.PR, task.Worktree = "https://github.com/o/r/pull/17", repo
	task.ReviewID, task.VerificationID = "retained-review", "retained-verification"
	record, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	for _, nextMode := range []string{model.AgenticMode, model.PersonalMode} {
		if err := e.Store.ActivateMode(ctx, nextMode); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Version != record.Version+2 {
		t.Fatalf("mode roundtrip cancelled task more than once: prior=%d cancelled=%d", record.Version, cancelled.Version)
	}
	assertCancelledPRPreserved(t, cancelled, task)
	remote := &cancellationGitHub{}
	if sourcePresent {
		remote.issues = []model.Issue{task.Issue}
	}
	e.GitHub = remote
	e.Adapter = func(model.Provider) provider.Adapter {
		t.Fatal("cancelled task invoked provider")
		return nil
	}
	e.Reconcile(ctx)
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	e.fail(ctx, task.ID, errors.New("late provider transport failure"))
	assertCancellationUnchanged(t, e, cancelled)
	if remote.issueCalls != 0 || remote.prCalls != 0 {
		t.Fatalf("cancelled task polled remote issue or PR: %+v", remote)
	}
	for _, kind := range []string{"outcomes", "sessions"} {
		rows, err := e.Store.List(ctx, kind)
		if err != nil || len(rows) != 0 {
			t.Fatalf("cancelled task created %s: %+v %v", kind, rows, err)
		}
	}
}

func assertCancelledPRPreserved(t *testing.T, record model.Record, original model.Task) {
	t.Helper()
	task, err := store.Decode[model.Task](record)
	if err != nil || !task.Cancelled() || task.PR != original.PR || task.Worktree != original.Worktree || task.ReviewID != original.ReviewID || task.VerificationID != original.VerificationID {
		t.Fatalf("mode cancellation lost published work or evidence: %+v %v", task, err)
	}
}

func assertCancellationUnchanged(t *testing.T, e *Engine, expected model.Record) {
	t.Helper()
	actual, err := e.Store.Get(context.Background(), "tasks", expected.ID)
	if err != nil || actual.Version != expected.Version || !bytes.Equal(actual.Data, expected.Data) {
		t.Fatalf("cancelled task changed: before=%+v after=%+v error=%v", expected, actual, err)
	}
	tasks, err := e.Store.List(context.Background(), "tasks")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("unchanged source redispatched cancelled work: %+v %v", tasks, err)
	}
}
