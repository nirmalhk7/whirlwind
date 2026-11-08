package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	e, task, project, record := acceptedDeliveryFixture(t)
	ctx := context.Background()
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
	finished, err := step.submitting()
	if err != nil || finished || step.task.Delivery == nil || step.task.Delivery.Stage != "prepared" {
		t.Fatalf("local delivery intent not prepared: finished=%v delivery=%+v err=%v", finished, step.task.Delivery, err)
	}
	if err := e.Store.Put(ctx, "tasks", step.task.ID, step.task, step.record.Version); err != nil {
		t.Fatal(err)
	}
	step.record.Version++
	delivery := *step.task.Delivery
	if commit, err := promoteReviewedSnapshot(ctx, delivery.OriginalWorktree, delivery.Worktree, delivery.Branch, delivery.Base, delivery.ReviewedCommit); err != nil || commit != delivery.ReviewedCommit {
		t.Fatalf("simulate promotion before supervisor acknowledgement: commit=%q err=%v", commit, err)
	}
	storePath := filepath.Join(e.Root, "state.db")
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := New(reopened, e.GitHub, e.Root)
	project, err = restarted.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, err := reopened.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Decode[model.Task](taskRecord)
	if err != nil {
		t.Fatal(err)
	}
	finalizer := &countingLocalDeliveryAdapter{}
	restarted.Adapter = func(model.Provider) provider.Adapter { return finalizer }
	step = &taskStep{Engine: restarted, ctx: ctx, project: project, task: task, record: taskRecord}
	finished, err = step.submitting()
	if err != nil || finished || step.task.Delivery.Stage != "snapshot_ready" {
		t.Fatalf("recovery did not recognize completed promotion: finished=%v delivery=%+v err=%v", finished, step.task.Delivery, err)
	}
	if err := reopened.Put(ctx, "tasks", step.task.ID, step.task, step.record.Version); err != nil {
		t.Fatal(err)
	}
	step.record.Version++
	if finished, err = step.submitting(); err != nil || !finished {
		t.Fatalf("recovered local delivery did not finish: finished=%v err=%v", finished, err)
	}
	completedRecord, err := reopened.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.Decode[model.Task](completedRecord)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Stage != "complete" || completed.Delivery == nil || completed.Delivery.Stage != "complete" || finalizer.calls != 1 {
		t.Fatalf("local delivery recovery duplicated or lost finalization: stage=%s delivery=%+v finalizer_calls=%d", completed.Stage, completed.Delivery, finalizer.calls)
	}
	if got := submissionGit(t, delivery.Worktree, "rev-parse", "HEAD"); got != delivery.ReviewedCommit {
		t.Fatalf("recovered worktree changed reviewed commit: got=%s want=%s", got, delivery.ReviewedCommit)
	}
	publications, err := reopened.List(ctx, "publications")
	if err != nil || len(publications) != 0 || completed.PR != "" {
		t.Fatalf("local delivery recovery created remote publication: task_pr=%q count=%d err=%v", completed.PR, len(publications), err)
	}
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
			return provider.Output{Text: answeredQuestionPlan(t)}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "continue", ReadOnly: true}); err != nil {
		rows, listErr := e.Store.List(ctx, "sessions")
		var attempts []string
		if listErr == nil {
			for _, row := range rows {
				attempt, decodeErr := store.Decode[model.Attempt](row)
				if decodeErr == nil && attempt.Task == task.ID {
					attempts = append(attempts, fmt.Sprintf("%s:%s:%s:%s", attempt.Role, attempt.Status, attempt.Error, attempt.ProcessState))
				}
			}
		}
		t.Fatalf("fresh attempt did not retain answered question: %v attempts=%v list_error=%v", err, attempts, listErr)
	}
	if !seen {
		t.Fatal("durable native answers absent from recovered input")
	}
}

func answeredQuestionPlan(t *testing.T) string {
	t.Helper()
	plan := model.Plan{
		Objective:                "Preserve the answered database choice",
		What:                     "Use the saved answer in planning",
		Why:                      "A fresh attempt must retain human decisions",
		Benefit:                  "The selected database remains explicit",
		Where:                    []string{"internal/workflow/persistence_group2_test.go"},
		Scope:                    []string{"Keep the answered choice in the plan"},
		Exclusions:               []string{"Do not ask the same answered question again"},
		Steps:                    []string{"Include the saved choice in the plan"},
		AcceptanceCriteria:       []string{"The plan preserves the saved SQLite choice"},
		Requirements:             []model.Requirement{{ID: "REQ-ANSWER-1", Outcome: "The plan includes the saved SQLite choice.", EvidenceMethod: "Inspect the generated plan."}},
		RepositoryTestCommands:   [][]string{{"sh", "-c", "test -f result.txt"}},
		RepositoryTestInventory:  "configured: .whirlwind.yaml checks",
		RepositoryBuildInventory: "none configured: .whirlwind.yaml has no build commands",
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
