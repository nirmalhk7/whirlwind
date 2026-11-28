package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const (
	terminalFixtureCancelledAttempt = "cancel-attempt"
	terminalFixtureEditIdea         = "idea.update"
	terminalFixtureProvider         = "fixture-acp"
	terminalFixtureRequestsFile     = "requests.jsonl"
	terminalFixtureResult           = "result.txt"
	terminalFixtureRunStart         = "2026-01-01T10:00:00Z"
)

const (
	fixtureProject       = "terminal-project"
	fixtureDraftIdea     = "draft-idea"
	fixtureCancelIdea    = "cancel-idea"
	fixtureArchiveIdea   = "rejected-idea"
	fixtureDraftPlan     = "draft-plan"
	fixturePausedPlan    = "paused-plan"
	fixtureCompletedPlan = "delivered-plan"
	fixtureQuestion      = "terminal-question"
	fixtureAttempt       = "paused-attempt"
	fixtureDeadline      = "2099-01-01T10:05:00Z"
	fixtureDraftTitle    = "Verify fixture output"
	fixtureEditedTitle   = "Verify revised fixture output"
)

// Drive the shipped dashboard through the production socket and command adapter
// while the test owns the workflow fixtures and prevents worker dispatch.
func TestSeededHumanWorkflowThroughPTY(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_SEEDED_PTY") != "1" {
		t.Skip("set WHIRLWIND_TEST_SEEDED_PTY=1 for seeded PTY acceptance (requires Python 3 and Vim)")
	}
	data := t.TempDir()
	s := openSeededTerminalStore(t, data)
	w := workflow.New(s, nil, data)
	w.PlansRoot = filepath.Join(data, "plans")
	hash := seedTerminalWorkflow(t, s, w, data)
	startSeededTerminalControl(t, s, w, data)
	python, err := exec.LookPath("python3")
	mustSeedTerminal(t, err)
	cmd := exec.Command(python, "../../scripts/dashboard-seeded-pty.py", data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		requests, _ := os.ReadFile(filepath.Join(data, terminalFixtureRequestsFile))
		t.Fatalf("seeded terminal failed: %v\n%s\nrequests:\n%s", err, output, requests)
	}
	t.Logf("%s", output)
	assertSeededTerminalDecisions(t, s, data, hash)
	assertTerminalQueueCommands(t, s, data)
}

func assertTerminalQueueCommands(t *testing.T, s *store.Store, data string) {
	t.Helper()
	client := control.NewClient(control.SocketPath(data))
	for _, tc := range []struct {
		key, operation, project string
		version                 int
		wantError               bool
	}{
		{"push-current", control.RoleQueuePushOperation, fixtureProject, 3, false},
		{"push-current", control.RoleQueuePushOperation, fixtureProject, 3, false},
		{"push-stale", control.RoleQueuePushOperation, fixtureProject, 2, true},
		{"pop-stale", control.RoleQueuePopOperation, fixtureProject, 2, true},
		{"push-scope", control.RoleQueuePushOperation, "other-project", 3, true},
	} {
		response, err := client.Call(t.Context(), control.Request{RequestID: tc.key, IdempotencyKey: tc.key, Operation: tc.operation,
			ProjectID: tc.project, PlanID: fixtureDraftPlan, ExpectedVersion: tc.version, Payload: json.RawMessage(`{"queue":"coding_tasks"}`)})
		if (err != nil) != tc.wantError {
			t.Fatalf("%s: response=%+v error=%v", tc.key, response, err)
		}
	}
	terminalFixtureRecord[model.ExecutionPlan](t, s, "execution_plans", fixtureDraftPlan, 3)
}

func startSeededTerminalControl(t *testing.T, s *store.Store, w *workflow.Engine, data string) {
	t.Helper()
	logFile, err := os.OpenFile(filepath.Join(data, terminalFixtureRequestsFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	mustSeedTerminal(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	dispatcher := &seededTerminalDispatcher{
		SupervisorDispatcher: control.SupervisorDispatcher{
			DB:             s.DB,
			Commands:       localCommandExecutor{api: httpapi.New(s, w, "")},
			DispatchHealth: func() model.DispatchHealth { return model.DispatchHealth{} },
		},
		store: s, data: data, journal: json.NewEncoder(logFile),
	}
	server, err := control.Listen(control.SocketPath(data), dispatcher)
	mustSeedTerminal(t, err)
	t.Cleanup(func() { _ = server.Close() })
}

type seededTerminalDispatcher struct {
	control.SupervisorDispatcher
	store   *store.Store
	data    string
	journal *json.Encoder
	mu      sync.Mutex
}

func (d *seededTerminalDispatcher) Handle(ctx context.Context, request control.Request) (control.Response, error) {
	if request.Operation == "inspect.records" && request.ResourceID == "projects" {
		if err := holdSeededSnapshot(ctx, d.data); err != nil {
			return control.Response{}, err
		}
	}
	if strings.HasPrefix(request.Operation, "inspect.") || request.Operation == "plan.document.read" {
		return d.SupervisorDispatcher.Handle(ctx, request)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// Let a concurrent write win after the form loads so the dispatcher
	// has to reject the stale version through its normal command path.
	if request.Operation == terminalFixtureEditIdea && request.ExpectedVersion == 1 {
		if err := bumpSeededIdea(ctx, d.store); err != nil {
			return control.Response{}, err
		}
	}
	response, err := d.SupervisorDispatcher.Handle(ctx, request)
	entry := terminalJournalEntry{Request: request}
	if err != nil {
		entry.Error = err.Error()
	}
	if logErr := d.journal.Encode(entry); logErr != nil {
		return control.Response{}, logErr
	}
	return response, err
}

// Only the test can write these files. Wait for a deliberately blocked load so
// the PTY can verify keyboard handling while inspection is in flight.
func holdSeededSnapshot(ctx context.Context, data string) error {
	gate := filepath.Join(data, "block-snapshot")
	if err := os.Rename(gate, gate+"-entered"); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(gate + "-release"); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
}

type terminalJournalEntry struct {
	Request control.Request
	Error   string
}

func openSeededTerminalStore(t *testing.T, data string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(data, "whirlwind.db"))
	mustSeedTerminal(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedTerminalWorkflow(t *testing.T, s *store.Store, w *workflow.Engine, data string) string {
	t.Helper()
	ctx := context.Background()
	save := func(kind, id string, record any) {
		t.Helper()
		if err := s.Put(ctx, kind, id, record, 0); err != nil {
			t.Fatalf("seed %s/%s: %v", kind, id, err)
		}
	}
	repo := filepath.Join(data, "sample-repo")
	mustSeedTerminal(t, os.Mkdir(repo, 0700))
	repoPolicy, hash := seedTerminalRepository(t, repo)
	project := model.Project{ID: fixtureProject, Name: "Terminal Fixture", Path: repo, PolicyHash: hash, Policy: repoPolicy}
	save("projects", project.ID, project)
	save("operations", "seeded-ideation", model.Operation{Kind: "ideation", Subject: project.ID, Stage: "complete", StartedAt: terminalFixtureRunStart})
	save("providers", terminalFixtureProvider, model.Provider{ID: terminalFixtureProvider, Kind: "acp", Command: []string{"/usr/bin/true"}})
	for _, role := range model.AgentRoles() {
		save("agents", role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: terminalFixtureProvider, Model: "fixture-model"}}})
	}
	idea := model.Idea{ID: fixtureDraftIdea, ProjectID: project.ID, RunID: "seeded-ideation",
		Title: "Draft export idea", Direction: "expand", What: "Export results", Why: "Keep reliable totals",
		Where: "reports", HowItHelps: "Keep accounting accurate", DecisionStatus: "draft", NoveltyMethod: "not_checked", NoveltyOutcome: "deferred"}
	save("ideas", idea.ID, idea)
	idea.ID, idea.Title, idea.Direction = fixtureCancelIdea, "Active planning idea", "simplify"
	save("ideas", idea.ID, idea)
	idea.DecisionStatus = "approved"
	mustSeedTerminal(t, s.Put(ctx, "ideas", idea.ID, idea, 1))
	cancelling := model.ExecutionPlan{ID: model.PlanID("idea:" + idea.ID), ProjectID: project.ID, IdeaID: idea.ID, IdeationRunID: idea.RunID,
		Issue: model.Issue{Title: "Cancel fixture planning"}, Stage: "planning", PlanVersion: 1}
	save("execution_plans", cancelling.ID, cancelling)
	save("sessions", terminalFixtureCancelledAttempt, model.Attempt{AttemptID: terminalFixtureCancelledAttempt, Task: cancelling.ID,
		ProjectID: project.ID, IdeaID: idea.ID, OperationID: idea.RunID, Role: "plan", Status: "running",
		StartedAt: terminalFixtureRunStart, DeadlineAt: fixtureDeadline, PlanVersion: 1})

	idea.ID, idea.Title, idea.Direction, idea.DecisionStatus = fixtureArchiveIdea, "Decline fixture proposal", "optimize", "draft"
	save("ideas", idea.ID, idea)

	draft := model.ExecutionPlan{ID: fixtureDraftPlan, ProjectID: project.ID, Mode: model.AgenticMode, PlanVersion: 1,
		Issue: model.Issue{Title: fixtureDraftTitle, Body: "Make result useful"}, Plan: seededTerminalSpecification(),
		Selection: &model.SelectionAuthorization{CandidateID: "draft-selection", CandidateVersion: 1, SelectedAt: terminalFixtureRunStart}}
	mustSeedTerminal(t, w.WriteLocalPlan(ctx, project, &draft))
	save("execution_plans", draft.ID, draft)

	paused := model.ExecutionPlan{ID: fixturePausedPlan, ProjectID: project.ID, Mode: model.AgenticMode,
		Stage: "paused", ResumeStage: "coding", PlanVersion: 2, Issue: model.Issue{Title: "Await fixture clarification"},
		Wait: &model.ExecutionPlanWait{Kind: "human_decision", AttemptID: fixtureAttempt, ResumeStage: "coding", PlanVersion: 2}}
	save("execution_plans", paused.ID, paused)
	save("sessions", fixtureAttempt, model.Attempt{AttemptID: fixtureAttempt, Task: paused.ID, ProjectID: project.ID,
		Role: "code", PlanVersion: paused.PlanVersion, Finished: true, Status: "awaiting_human",
		StartedAt: terminalFixtureRunStart, DeadlineAt: fixtureDeadline, RestartCount: 2})
	save("questions", fixtureQuestion, model.QuestionSet{ID: fixtureQuestion, TaskID: paused.ID, AttemptID: fixtureAttempt,
		PlanVersion: paused.PlanVersion, Status: "pending", Questions: []model.Question{{ID: "encoding", Text: "Which fixture encoding?"}}})

	delivered := model.ExecutionPlan{ID: fixtureCompletedPlan, ProjectID: project.ID, Stage: "complete", PlanVersion: 3,
		Issue: model.Issue{Title: "Inspect completed fixture"}, ReviewedCommit: "reviewed-fixture",
		ReviewID: "fixture-review", VerificationID: "fixture-verification",
		Delivery: &model.LocalDelivery{Stage: "complete", Branch: "delivery/fixture", DeliveryCommit: "delivered-fixture",
			Diff: "+expected fixture output", VerificationID: "fixture-verification", ReviewID: "fixture-review",
			UnmetRequirements: []string{"REQ-OPTIONAL"}, Adherence: &model.Adherence{Score: 90}}}
	save("execution_plans", delivered.ID, delivered)
	save("verification", delivered.VerificationID, map[string]any{
		"plan_id": delivered.ID, "passed": true, "checks": []map[string]any{{"name": "fixture-regression", "passed": true}},
	})
	return draft.PlanDocument.Hash
}

func seedTerminalRepository(t *testing.T, repo string) (model.Policy, string) {
	t.Helper()
	const committed = "risk_rules: [{level: high, paths: [other.txt]}]\nallowed_paths: [result.txt]\nchecks:\n  - name: result-content\n    command: [sh, -c, 'test \"$(cat result.txt)\" = correct']\n"
	mustSeedTerminal(t, os.WriteFile(filepath.Join(repo, ".whirlwind.yml"), []byte(committed), 0600))
	mustSeedTerminal(t, os.WriteFile(filepath.Join(repo, terminalFixtureResult), []byte("incorrect\n"), 0600))
	for _, args := range [][]string{{"init", "--template="}, {"add", ".whirlwind.yml", terminalFixtureResult}, {"-c", "user.name=Terminal Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--no-gpg-sign", "-m", "Seed fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	value, digest, err := policy.Load(repo)
	mustSeedTerminal(t, err)
	return value, digest
}

func seededTerminalSpecification() *model.PlanSpecification {
	return &model.PlanSpecification{
		Objective: fixtureDraftTitle, What: "Correct the fixture result.", Why: "Result incomplete.", Benefit: "Useful output.",
		Where: []string{terminalFixtureResult}, Scope: []string{"Correct the result value."}, Exclusions: []string{"Keep the result file format."},
		Steps:                    []string{"Add a failing assertion.", "Correct the result and rerun the assertion."},
		RepositoryTestInventory:  "None configured: the fixture has no repository test commands.",
		RepositoryBuildInventory: "None configured: the fixture has no repository build commands.",
		AcceptanceCriteria:       []string{"Result parses correctly."}, Requirements: []model.Requirement{{ID: "REQ-RESULT",
			Outcome: "result.txt contains correct.", EvidenceMethod: "Run result-content and inspect its exit status."}},
		Checks: []model.Check{{Name: "result-content", Command: []string{"sh", "-c", "test \"$(cat result.txt)\" = correct"}}},
		Units:  []model.Unit{{ID: "main", Objective: "Test and implement result", AllowedPaths: []string{terminalFixtureResult}}},
	}
}

func bumpSeededIdea(ctx context.Context, s *store.Store) error {
	record, err := s.Get(ctx, "ideas", fixtureDraftIdea)
	if err != nil {
		return err
	}
	if record.Version != 1 {
		return fmt.Errorf("want first form at version 1, found %d", record.Version)
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return err
	}
	idea.Why = "Background update preserved"
	idea.Title = "Draft advanced elsewhere"
	return s.Put(ctx, "ideas", idea.ID, idea, record.Version)
}

func assertSeededTerminalDecisions(t *testing.T, s *store.Store, data, initialHash string) {
	t.Helper()
	writes := assertSeededTerminalJournal(t, data)
	idea := terminalFixtureRecord[model.Idea](t, s, "ideas", fixtureDraftIdea, 4)
	if idea.DecisionStatus != "approved" || idea.Why != "Background update preserved" ||
		strings.Contains(idea.What, "STALE") || !strings.Contains(idea.What, "APPROVED") {
		t.Fatalf("draft write, stale protection, or distinct idea approval lost: %+v", idea)
	}
	cancelled := terminalFixtureRecord[model.Idea](t, s, "ideas", fixtureCancelIdea, 4)
	if cancelled.DecisionStatus != "error" || cancelled.ErrorReason != "user_cancelled" {
		t.Fatalf("planner cancellation did not persist: %+v", cancelled)
	}
	cancelledPlan := terminalFixtureRecord[model.ExecutionPlan](t, s, "execution_plans", model.PlanID("idea:"+fixtureCancelIdea), 2)
	cancelledAttempt := terminalFixtureRecord[model.Attempt](t, s, "sessions", terminalFixtureCancelledAttempt, 2)
	if cancelledPlan.Stage != "cancelled" || cancelledAttempt.Status != "cancelled" || !cancelledAttempt.Finished {
		t.Fatalf("planner cancellation left plan or attempt active: stage=%s attempt=%s", cancelledPlan.Stage, cancelledAttempt.Status)
	}
	archived := terminalFixtureRecord[model.Idea](t, s, "ideas", fixtureArchiveIdea, 2)
	if archived.ArchivedAt == "" || archived.ArchiveReason != "Outside this smoke test" || archived.PlanID != "" {
		t.Fatalf("archiving an idea did not record the refusal: %+v", archived)
	}
	plans, err := s.List(t.Context(), "execution_plans")
	mustSeedTerminal(t, err)
	for _, record := range plans {
		plan, err := store.Decode[model.ExecutionPlan](record)
		mustSeedTerminal(t, err)
		if plan.IdeaID == archived.ID {
			t.Fatalf("archived idea created plan %s", plan.ID)
		}
	}
	plan := terminalFixtureRecord[model.ExecutionPlan](t, s, "execution_plans", fixtureDraftPlan, 3)
	save, approve := writes[5].Request, writes[6].Request
	if save.ContentHash != initialHash || approve.ContentHash == initialHash ||
		plan.PlanDocument.ApprovedHash != approve.ContentHash ||
		plan.Plan.Objective != fixtureEditedTitle || plan.Stage != "coding" {
		t.Fatalf("Vim save/approval did not bind separate document identities: %+v; %+v", plan, writes[5:7])
	}
	q := terminalFixtureRecord[model.QuestionSet](t, s, "questions", fixtureQuestion, 2)
	if q.Status != "answered" || q.Answers["encoding"] != "UTF-8" {
		t.Fatalf("saved clarification not answered: %+v", q)
	}
	attemptRecord, err := s.Get(context.Background(), "sessions", fixtureAttempt)
	mustSeedTerminal(t, err)
	attempt, err := store.Decode[model.Attempt](attemptRecord)
	mustSeedTerminal(t, err)
	if attempt.DeadlineAt != fixtureDeadline || attempt.RestartCount != 2 {
		t.Fatalf("reopening or answering reset attempt deadline/retry history: %+v", attempt)
	}
	terminalFixtureRecord[model.ExecutionPlan](t, s, "execution_plans", fixtureCompletedPlan, 1)
}

func assertSeededTerminalJournal(t *testing.T, data string) []terminalJournalEntry {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(data, terminalFixtureRequestsFile))
	mustSeedTerminal(t, err)
	var writes []terminalJournalEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		var entry terminalJournalEntry
		mustSeedTerminal(t, json.Unmarshal([]byte(line), &entry))
		if entry.Request.RequestID == "" || entry.Request.IdempotencyKey == "" || seen[entry.Request.IdempotencyKey] {
			t.Fatalf("missing or replayed decision identity: %+v", entry)
		}
		seen[entry.Request.IdempotencyKey] = true
		writes = append(writes, entry)
	}
	expected := []struct {
		operation, resource, plan string
		version                   int
		stale                     bool
	}{
		{terminalFixtureEditIdea, fixtureDraftIdea, "", 1, true},
		{terminalFixtureEditIdea, fixtureDraftIdea, "", 2, false},
		{"idea.approve", fixtureDraftIdea, "", 3, false},
		{"planner.cancel", fixtureCancelIdea, "", 3, false},
		{"idea.archive", fixtureArchiveIdea, "", 1, false},
		{"plan.document.save", "", fixtureDraftPlan, 1, false},
		{"plan.review", "", fixtureDraftPlan, 2, false},
		{"question.answer", fixtureQuestion, fixturePausedPlan, 1, false},
	}
	if len(writes) != len(expected) {
		t.Fatalf("unexpected mutations/replay: %+v", writes)
	}
	for i, want := range expected {
		got := writes[i]
		r := got.Request
		if r.Operation != want.operation || r.ResourceID != want.resource || r.PlanID != want.plan ||
			r.ExpectedVersion != want.version || (got.Error != "") != want.stale {
			t.Fatalf("decision %d: got %+v want %+v", i, got, want)
		}
	}
	return writes
}

func terminalFixtureRecord[T any](t *testing.T, s *store.Store, kind, id string, wantVersion int) T {
	t.Helper()
	record, err := s.Get(context.Background(), kind, id)
	mustSeedTerminal(t, err)
	if record.Version != wantVersion {
		t.Fatalf("%s %s at version %d; want %d", kind, id, record.Version, wantVersion)
	}
	value, err := store.Decode[T](record)
	mustSeedTerminal(t, err)
	return value
}

func mustSeedTerminal(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
