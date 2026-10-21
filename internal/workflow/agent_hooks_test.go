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

func TestAgentQuestionsWaitForAnswers(t *testing.T) {
	a := hookAttempt(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan hookResult, 1)
	go func() {
		answers, err := a.questions(ctx, []model.Question{{ID: "db", Text: "Which database?"}})
		done <- hookResult{answers: answers, err: err}
	}()
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
	result := <-done
	if result.err != nil || !reflect.DeepEqual(result.answers, set.Answers) {
		t.Fatalf("answers lost: %+v", result)
	}
}

func TestAgentPermissionWaitsForDecision(t *testing.T) {
	for _, decision := range []string{"allow", "deny"} {
		t.Run(decision, func(t *testing.T) { assertPermissionDecision(t, decision) })
	}
}

func assertPermissionDecision(t *testing.T, decision string) {
	t.Helper()
	a := hookAttempt(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan hookResult, 1)
	go func() {
		allowed, err := a.permission(ctx, provider.Permission{Command: []string{"not-allowed"}})
		done <- hookResult{allowed: allowed, err: err}
	}()
	record := waitHookRecord(t, a, "permissions")
	value, err := store.Decode[map[string]any](record)
	if err != nil {
		t.Fatal(err)
	}
	value["decision"] = decision
	if err = a.Store.Put(ctx, "permissions", record.ID, value, record.Version); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.allowed != (decision == "allow") {
		t.Fatalf("decision lost: %+v", result)
	}
}

func TestAgentCancellationClosesPendingRequests(t *testing.T) {
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
	if err := <-done; !errors.Is(err, context.Canceled) {
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
	if value.Status != "cancelled" && value.Decision != "cancelled" {
		t.Fatalf("request remained pending: %s", record.Data)
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
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("permission cancellation lost: %v", err)
	}
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
