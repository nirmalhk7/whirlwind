package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type hookResult struct {
	answers map[string]string
	allowed bool
	err     error
}

func hookAttempt(t *testing.T) *agentAttempt {
	t.Helper()
	e, task, path := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return &agentAttempt{agentRun: &agentRun{Engine: e, task: task, project: project, input: provider.Input{CWD: path}}, chosen: model.Provider{ID: "fake"}}
}

func waitHookRecord(t *testing.T, a *agentAttempt, kind string) model.Record {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		records, err := a.Store.List(context.Background(), kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) > 0 {
			return records[0]
		}
		select {
		case <-deadline:
			t.Fatal("agent request was not stored")
		case <-ticker.C:
		}
	}
}

func TestAgentQuestionsEndAttemptAndPersistForLaterAnswer(t *testing.T) {
	a := hookAttempt(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answers, err := a.questions(ctx, []model.Question{{ID: "db", Text: "Which database?"}})
	if !errors.Is(err, ErrHumanDecisionPending) || answers != nil {
		t.Fatalf("question did not end the attempt: answers=%v err=%v", answers, err)
	}
	record := waitHookRecord(t, a, "questions")
	set, err := store.Decode[model.QuestionSet](record)
	if err != nil {
		t.Fatal(err)
	}
	set.Status = "answered"
	set.Answers = map[string]string{"db": "SQLite"}
	if err = a.Store.Put(ctx, "questions", record.ID, set, record.Version); err != nil {
		t.Fatal(err)
	}
	reopened, err := a.Store.Get(ctx, "questions", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, err = store.Decode[model.QuestionSet](reopened)
	if err != nil || !reflect.DeepEqual(set.Answers, map[string]string{"db": "SQLite"}) {
		t.Fatalf("human answer not preserved: %+v %v", set, err)
	}
}

func TestAgentPermissionEndsAttemptAndPersistsForLaterDecision(t *testing.T) {
	for _, decision := range []string{"allow", "deny"} {
		t.Run(decision, func(t *testing.T) { assertPermissionDecision(t, decision) })
	}
}

func TestRemotePublicationCommandsCannotBeAllowedByCommandPolicy(t *testing.T) {
	commands := [][]string{
		{"git", "push", "origin", "HEAD"},
		{"gh", "pr", "create", "--title", "change"},
		{"bash", "-c", "git push origin HEAD"},
		{"env", "gh", "issue", "close", "17"},
		{"glab", "mr", "merge", "17"},
	}
	for _, command := range commands {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			a := hookAttempt(t)
			a.project.Policy.Commands = [][]string{{command[0]}}
			allowed, err := a.permission(context.Background(), provider.Permission{Tool: "terminal/create", Command: command})
			if err != nil || allowed {
				t.Fatalf("remote publication command was allowed: allowed=%v err=%v", allowed, err)
			}
		})
	}
}

func TestRemotePublicationToolsCannotBeAllowedByCommandPolicy(t *testing.T) {
	a := hookAttempt(t)
	a.project.Policy.Commands = [][]string{{"github"}}
	allowed, err := a.permission(context.Background(), provider.Permission{Tool: "github/create_pull_request"})
	if err != nil || allowed {
		t.Fatalf("remote publication integration was allowed: allowed=%v err=%v", allowed, err)
	}
}

func TestRemoteMutationClassifierPreservesLocalCommandsAndReads(t *testing.T) {
	for _, command := range [][]string{{"git", "status", "--short"}, {"git", "commit", "-m", "local"}, {"gh", "pr", "view", "17"}, {"go", "test", "./..."}, {"curl", "https://example.test/read"}} {
		if remotePublicationMutation(provider.Permission{Command: command}) {
			t.Errorf("local or read-only command was classified as remote mutation: %q", command)
		}
	}
	for _, command := range [][]string{{"gh", "api", "--method", "POST", "/repos/o/r/issues"}, {"curl", "-X", "DELETE", "https://example.test/resource"}, {"bash", "-c", "git status && gh pr merge 17"}} {
		if !remotePublicationMutation(provider.Permission{Command: command}) {
			t.Errorf("remote mutation was not recognized: %q", command)
		}
	}
}

func assertPermissionDecision(t *testing.T, decision string) {
	t.Helper()
	a := hookAttempt(t)
	ctx := context.Background()
	allowed, err := a.permission(ctx, provider.Permission{Command: []string{"not-allowed"}})
	if allowed || !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatalf("permission did not end the attempt: allowed=%v err=%v", allowed, err)
	}
	record := waitHookRecord(t, a, "permissions")
	value, err := store.Decode[map[string]any](record)
	if err != nil {
		t.Fatal(err)
	}
	value["decision"] = decision
	if err = a.Store.Put(ctx, "permissions", record.ID, value, record.Version); err != nil {
		t.Fatal(err)
	}
	updated, err := a.Store.Get(ctx, "permissions", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err = store.Decode[map[string]any](updated)
	if err != nil || value["decision"] != decision {
		t.Fatalf("decision lost: %v %v", value, err)
	}
}

func TestAgentCancellationPreservesPendingRequests(t *testing.T) {
	for _, kind := range []string{"questions", "permissions"} {
		t.Run(kind, func(t *testing.T) { assertHookCancelled(t, kind) })
	}
}

func assertHookCancelled(t *testing.T, kind string) {
	t.Helper()
	a := hookAttempt(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- requestHook(a, ctx, kind) }()
	record := waitHookRecord(t, a, kind)
	cancel()
	if err := <-done; !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatalf("cancellation lost: %v", err)
	}
	record, err := a.Store.Get(context.Background(), kind, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Status   string `json:"status"`
		Decision string `json:"decision"`
	}
	if err = json.Unmarshal(record.Data, &value); err != nil {
		t.Fatal(err)
	}
	if value.Status != "pending" && value.Decision != "pending" {
		t.Fatalf("pending human intent was lost: %s", record.Data)
	}
}

func TestPendingHumanDecisionDoesNotFallbackToNextProvider(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
	calls := 0
	started := make(chan struct{})
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, hooks provider.Hooks) (provider.Output, error) {
			calls++
			close(started)
			_, err := hooks.Questions(ctx, []model.Question{{ID: "q", Text: "Choose"}})
			return provider.Output{}, err
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.run(ctx); done <- err }()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("pending question fell through to another model: calls=%d err=%v", calls, err)
	}
	questions, err := h.engine.Store.List(context.Background(), "questions")
	if err != nil || len(questions) != 1 {
		t.Fatalf("pending question not saved: count=%d err=%v", len(questions), err)
	}
	set, err := store.Decode[model.QuestionSet](questions[0])
	if err != nil || set.Status != "pending" {
		t.Fatalf("pending question was not preserved: %+v %v", set, err)
	}
}

func requestHook(a *agentAttempt, ctx context.Context, kind string) error {
	if kind == "questions" {
		_, err := a.questions(ctx, []model.Question{{ID: "q", Text: "Choose"}})
		return err
	}
	_, err := a.permission(ctx, provider.Permission{Command: []string{"not-allowed"}})
	return err
}

func TestPermissionSnapshotsExcludeProtocolSecrets(t *testing.T) {
	a := hookAttempt(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.permission(ctx, provider.Permission{Tool: "terminal/create", Command: []string{"unsafe", "--token=private-command"}, Raw: json.RawMessage(`{"env":[{"name":"TOKEN","value":"private-env"}],"authorization":"private-auth","command":"unsafe"}`)})
		done <- err
	}()
	record := waitHookRecord(t, a, "permissions")
	cancel()
	<-done
	for _, secret := range []string{"private-env", "private-auth", "private-command"} {
		if strings.Contains(string(record.Data), secret) {
			t.Errorf("permission snapshot retained %s", secret)
		}
		var count int
		if err := a.Store.DB.QueryRow("SELECT count(*) FROM events WHERE data LIKE ?", "%"+secret+"%").Scan(&count); err != nil || count != 0 {
			t.Errorf("SSE retained %s: %d %v", secret, count, err)
		}
	}
}
