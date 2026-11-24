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
	"whirlwind/internal/dashboard"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const (
	fixtureProject       = "terminal-project"
	fixtureDraftIdea     = "draft-idea"
	fixtureCancelIdea    = "cancel-idea"
	fixtureDraftPlan     = "draft-plan"
	fixturePausedPlan    = "paused-plan"
	fixtureCompletedPlan = "delivered-plan"
	fixtureQuestion      = "terminal-question"
	fixtureAttempt       = "paused-attempt"
	fixtureDeadline      = "2099-01-01T10:05:00Z"
	fixtureDraftTitle    = "Verify fixture output"
	fixtureEditedTitle   = "Verify revised fixture output"
)

// This intentionally uses the production dashboard and dispatcher/command
// adapter, but without a Unix listener or provider. The separate launcher PTY
// acceptance checks the real process/socket boundary.
func TestSeededHumanWorkflowThroughPTY(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_SEEDED_PTY") != "1" {
		t.Skip("set WHIRLWIND_TEST_SEEDED_PTY=1 for seeded PTY acceptance (requires Python 3 and Vim)")
	}
	t.Setenv("WHIRLWIND_SEEDED_PTY_DATA", "")
	data := t.TempDir()
	s := openSeededTerminalStore(t, data)
	w := workflow.New(s, nil, data)
	w.PlansRoot = filepath.Join(data, "plans")
	hash := seedTerminalWorkflow(t, s, w, data)
	executable, err := os.Executable()
	mustSeedTerminal(t, err)
	python, err := exec.LookPath("python3")
	mustSeedTerminal(t, err)
	cmd := exec.Command(python, "../../scripts/dashboard-seeded-pty.py", data, executable)
	output, err := cmd.CombinedOutput()
	if err != nil {
		requests, _ := os.ReadFile(filepath.Join(data, "requests.jsonl"))
		t.Fatalf("seeded terminal failed: %v\n%s\nrequests:\n%s", err, output, requests)
	}
	t.Logf("%s", output)
	assertSeededTerminalDecisions(t, s, data, hash)
}

// TestSeededHumanWorkflowTerminalHelper runs only inside the harness PTYs.
func TestSeededHumanWorkflowTerminalHelper(t *testing.T) {
	data := os.Getenv("WHIRLWIND_SEEDED_PTY_DATA")
	if data == "" {
		return
	}
	s := openSeededTerminalStore(t, data)
	w := workflow.New(s, nil, data)
	w.PlansRoot = filepath.Join(data, "plans")
	dispatcher := control.SupervisorDispatcher{DB: s.DB, Commands: localCommandExecutor{api: httpapi.New(s, w, "")}}
	logFile, err := os.OpenFile(filepath.Join(data, "requests.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	mustSeedTerminal(t, err)
	defer logFile.Close()
	log := json.NewEncoder(logFile)
	var serial sync.Mutex
	app := dashboard.New(data)
	app.SetCommandHandler(func(ctx context.Context, request control.Request) (json.RawMessage, error) {
		serial.Lock()
		defer serial.Unlock()
		// Simulate a concurrent writer winning after the form's last refresh.
		// The actual dispatcher, not the UI test fake, rejects the stale write.
		if request.Operation == "idea.update" && request.ExpectedVersion == 1 {
			if err := bumpSeededIdea(ctx, s); err != nil {
				return nil, err
			}
		}
		response, err := dispatcher.Handle(ctx, request)
		entry := terminalJournalEntry{Request: request}
		if err != nil {
			entry.Error = err.Error()
		}
		if logErr := log.Encode(entry); logErr != nil {
			return nil, logErr
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(response.Result)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := app.Watch(ctx, func(ctx context.Context) (control.Snapshot, error) {
		if err := holdSeededSnapshot(ctx, data); err != nil {
			return nil, err
		}
		result, err := dispatcher.Snapshot(ctx, 100)
		if err == nil {
			result["supervisor"] = []model.Record{{Data: json.RawMessage("{\"online\":true}")}}
		}
		return result, err
	})
	err = app.Run()
	cancel()
	<-done
	mustSeedTerminal(t, err)
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
	save("operations", "seeded-ideation", model.Operation{Kind: "ideation", Subject: project.ID, Stage: "complete", StartedAt: "2026-01-01T10:00:00Z"})
	save("providers", "fixture-acp", model.Provider{ID: "fixture-acp", Kind: "acp", Command: []string{"/usr/bin/true"}})
	for _, role := range model.AgentRoles() {
		save("agents", role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: "fixture-acp", Model: "fixture-model"}}})
	}
	idea := model.Idea{ID: fixtureDraftIdea, ProjectID: project.ID, RunID: "seeded-ideation",
		Title: "Draft export idea", Direction: "expand", What: "Export results", Why: "Keep reliable totals",
		Where: "reports", HowItHelps: "Keep accounting accurate", DecisionStatus: "draft", NoveltyMethod: "not_checked", NoveltyOutcome: "deferred"}
	save("ideas", idea.ID, idea)
	idea.ID, idea.Title, idea.Direction = fixtureCancelIdea, "Active planning idea", "simplify"
	save("ideas", idea.ID, idea)
	idea.DecisionStatus = "approved"
	mustSeedTerminal(t, s.Put(ctx, "ideas", idea.ID, idea, 1))
	cancelling := model.ExecutionPlan{ID: "cancel-plan", ProjectID: project.ID, IdeaID: idea.ID, IdeationRunID: idea.RunID,
		Issue: model.Issue{Title: "Cancel fixture planning"}, Stage: "planning", PlanVersion: 1}
	save("execution_plans", cancelling.ID, cancelling)
	save("sessions", "cancel-attempt", model.Attempt{AttemptID: "cancel-attempt", Task: cancelling.ID,
		ProjectID: project.ID, IdeaID: idea.ID, OperationID: idea.RunID, Role: "plan", Status: "running",
		StartedAt: "2026-01-01T10:00:00Z", DeadlineAt: fixtureDeadline, PlanVersion: 1})

	draft := model.ExecutionPlan{ID: fixtureDraftPlan, ProjectID: project.ID, Mode: model.AgenticMode, PlanVersion: 1,
		Issue: model.Issue{Title: fixtureDraftTitle, Body: "Make result useful"}, Plan: seededTerminalSpecification()}
	mustSeedTerminal(t, w.WriteLocalPlan(ctx, project, &draft))
	save("execution_plans", draft.ID, draft)

	paused := model.ExecutionPlan{ID: fixturePausedPlan, ProjectID: project.ID, Mode: model.AgenticMode,
		Stage: "paused", ResumeStage: "coding", PlanVersion: 2, Issue: model.Issue{Title: "Await fixture clarification"},
		Wait: &model.ExecutionPlanWait{Kind: "human_decision", AttemptID: fixtureAttempt, ResumeStage: "coding", PlanVersion: 2}}
	save("execution_plans", paused.ID, paused)
	save("sessions", fixtureAttempt, model.Attempt{AttemptID: fixtureAttempt, Task: paused.ID, ProjectID: project.ID,
		Role: "code", PlanVersion: paused.PlanVersion, Finished: true, Status: "awaiting_human",
		StartedAt: "2026-01-01T10:00:00Z", DeadlineAt: fixtureDeadline, RestartCount: 2})
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
	mustSeedTerminal(t, os.WriteFile(filepath.Join(repo, "result.txt"), []byte("incorrect\n"), 0600))
	for _, args := range [][]string{{"init", "--template="}, {"add", ".whirlwind.yml", "result.txt"}, {"-c", "user.name=Terminal Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--no-gpg-sign", "-m", "Seed fixture"}} {
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
		Where: []string{"result.txt"}, Scope: []string{"Correct the result value."}, Exclusions: []string{"Keep the result file format."},
		Steps:                    []string{"Add a failing assertion.", "Correct the result and rerun the assertion."},
		RepositoryTestInventory:  "None configured: the fixture has no repository test commands.",
		RepositoryBuildInventory: "None configured: the fixture has no repository build commands.",
		AcceptanceCriteria:       []string{"Result parses correctly."}, Requirements: []model.Requirement{{ID: "REQ-RESULT",
			Outcome: "result.txt contains correct.", EvidenceMethod: "Run result-content and inspect its exit status."}},
		Checks: []model.Check{{Name: "result-content", Command: []string{"sh", "-c", "test \"$(cat result.txt)\" = correct"}}},
		Units:  []model.Unit{{ID: "main", Objective: "Test and implement result", AllowedPaths: []string{"result.txt"}}},
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
	contents, err := os.ReadFile(filepath.Join(data, "requests.jsonl"))
	mustSeedTerminal(t, err)
	var writes []terminalJournalEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		var entry terminalJournalEntry
		mustSeedTerminal(t, json.Unmarshal([]byte(line), &entry))
		if entry.Request.Operation == "inspect.plan" {
			continue
		}
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
		{"idea.update", fixtureDraftIdea, "", 1, true},
		{"idea.update", fixtureDraftIdea, "", 2, false},
		{"idea.approve", fixtureDraftIdea, "", 3, false},
		{"planner.cancel", fixtureCancelIdea, "", 3, false},
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
	idea := terminalFixtureRecord[model.Idea](t, s, "ideas", fixtureDraftIdea, 4)
	if idea.DecisionStatus != "approved" || idea.Why != "Background update preserved" ||
		strings.Contains(idea.What, "STALE") || !strings.Contains(idea.What, "APPROVED") {
		t.Fatalf("draft write, stale protection, or distinct idea approval lost: %+v", idea)
	}
	cancelled := terminalFixtureRecord[model.Idea](t, s, "ideas", fixtureCancelIdea, 4)
	if cancelled.DecisionStatus != "error" || cancelled.ErrorReason != "user_cancelled" {
		t.Fatalf("planner cancellation did not persist: %+v", cancelled)
	}
	plan := terminalFixtureRecord[model.ExecutionPlan](t, s, "execution_plans", fixtureDraftPlan, 3)
	if writes[4].Request.ContentHash != initialHash || writes[5].Request.ContentHash == initialHash ||
		plan.PlanDocument.ApprovedHash != writes[5].Request.ContentHash ||
		plan.Plan.Objective != fixtureEditedTitle || plan.Stage != "coding" {
		t.Fatalf("Vim save/approval did not bind separate document identities: %+v; %+v", plan, writes[4:6])
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
