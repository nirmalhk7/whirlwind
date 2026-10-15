package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type evidenceAgent struct {
	store *store.Store
	t     *testing.T
}

func (a evidenceAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	records, err := a.store.List(ctx, "sessions")
	if err != nil || len(records) != 1 {
		a.t.Fatalf("attempt must exist before execution: %v", err)
	}
	var state map[string]any
	_ = json.Unmarshal(records[0].Data, &state)
	if state["prompt_artifact"] == nil || state["policy_hash"] == nil || state["attempt_id"] != records[0].ID {
		a.t.Errorf("run input/provenance missing: %s", records[0].Data)
	}
	h.Event("session/update", map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "partial reply"}}})
	h.Event("item/agentMessage/delta", map[string]string{"delta": "visible reply"})
	return provider.Output{Text: "final reply", SessionID: "native-s"}, nil
}
func TestAttemptPersistsInputStreamsAndMetrics(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter { return evidenceAgent{e.Store, t} }
	out, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "exact request", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ := e.Store.List(ctx, "sessions")
	metrics, _ := e.Store.List(ctx, "metrics")
	var metric map[string]any
	_ = json.Unmarshal(metrics[0].Data, &metric)
	if metric["attempt_id"] != sessions[0].ID {
		t.Fatalf("metrics not linked: %s", metrics[0].Data)
	}
	if out.Text != "final reply" {
		t.Fatal(out)
	}
	var n int
	if err = e.Store.DB.QueryRow("SELECT count(*) FROM attempt_events WHERE attempt_id=?", sessions[0].ID).Scan(&n); err != nil || n < 2 {
		t.Fatalf("stream evidence missing: %d %v", n, err)
	}
}
func TestAttemptCompletionRollsBackWhenMetricsFail(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_metric BEFORE INSERT ON resources WHEN NEW.kind='metrics' BEGIN SELECT RAISE(ABORT,'injected metric failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "request", ReadOnly: true})
	if err == nil {
		t.Fatal("required evidence failure swallowed")
	}
	records, _ := e.Store.List(ctx, "sessions")
	var value struct{ Finished bool }
	_ = json.Unmarshal(records[0].Data, &value)
	if value.Finished {
		t.Fatal("completion committed without metrics")
	}
}
func TestAutomaticPermissionIsAudited(t *testing.T) {
	a := hookAttempt(t)
	a.project.Policy.Commands = [][]string{{"safe"}}
	allowed, err := a.permission(context.Background(), provider.Permission{Command: []string{"safe"}})
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	records, _ := a.Store.List(context.Background(), "audit")
	if len(records) == 0 {
		t.Fatal("automatic permission has no decision evidence")
	}
}

func TestPublicationIntentExistsBeforeRemoteWrite(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	_, _ = e.Store.DB.Exec("DELETE FROM issue_tasks WHERE task_id=?", task.ID)
	task.Issue.Number = 0
	task.Stage = "plan_ready"
	task.Plan = &model.Plan{Objective: "publish", AcceptanceCriteria: []string{"exists"}}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	if err := e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	e.GitHub = publicationProbe{fakeGitHub: fakeGitHub{}, store: e.Store, t: t}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	records, _ := e.Store.List(ctx, "publications")
	if len(records) != 1 {
		t.Fatal("publication result missing")
	}
	_ = p
}

type publicationProbe struct {
	fakeGitHub
	store *store.Store
	t     *testing.T
}

func (g publicationProbe) CreateIssue(ctx context.Context, repo, title, body, marker string) (model.Issue, error) {
	records, _ := g.store.List(ctx, "publications")
	if len(records) != 1 {
		g.t.Error("remote write preceded durable intent")
	}
	return model.Issue{Number: 2, Title: title, Body: body + "\n\n" + marker, State: "OPEN", Labels: []string{"whirlwind:plan"}}, nil
}
func TestVerificationEvidenceFailureBlocksProgress(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_verification BEFORE INSERT ON resources WHEN NEW.kind='verification' BEGIN SELECT RAISE(ABORT,'verification unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Step(ctx, task.ID); err == nil {
		t.Fatal("verification persistence failure accepted")
	}
}

func TestCancelledAttemptRetainsTeardownEvidence(t *testing.T) {
	e, task, path := testEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, nil
		})
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			cancel()
			h.Event("whirlwind/process_exit", map[string]any{"exit_code": -1})
			return provider.Output{}, context.Canceled
		})
	}
	out, _ := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "request", ReadOnly: true})
	events, err := e.Store.AttemptEvents(context.Background(), out.AttemptID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "whirlwind/process_exit" {
			found = true
		}
	}
	if !found {
		t.Fatal("teardown evidence lost after cancellation")
	}
}
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
	for i := 0; i < 3; i++ {
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

func TestPublicationRejectsChangedCommitPolicyAndPlan(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	task.Base = "base-commit"
	task.ReviewedCommit = "reviewed-commit"
	task.VerificationID = "checks"
	task.ReviewID = "review"
	proof := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Base: task.Base, Head: task.ReviewedCommit, PolicyHash: "reviewed-policy", VerificationID: task.VerificationID, ReviewID: task.ReviewID}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": proof, "report": verification.Report{Passed: true}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, model.Review{Score: 9, Provenance: &proof}, 0); err != nil {
		t.Fatal(err)
	}
	task.Approved = true
	task.Approval = &proof
	if _, err := PublicationEvidence(ctx, e.Store, task, proof.PolicyHash); err != nil || !approvalMatches(task, proof.PolicyHash) {
		t.Fatal("current evidence rejected", err)
	}
	for _, change := range []string{"commit", "policy", "plan", "base"} {
		t.Run(change, func(t *testing.T) {
			changed := task
			hash := proof.PolicyHash
			switch change {
			case "commit":
				changed.ReviewedCommit = "new-commit"
			case "policy":
				hash = "new-policy"
			case "plan":
				changed.PlanVersion++
			case "base":
				changed.Base = "new-base"
			}
			if _, err := PublicationEvidence(ctx, e.Store, changed, hash); err == nil {
				t.Fatal("stale evidence accepted")
			}
			if approvalMatches(changed, hash) {
				t.Fatal("stale approval accepted")
			}
		})
	}
}
