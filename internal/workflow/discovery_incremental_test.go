package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

const (
	discoveryPolicyFile = ".whirlwind.yaml"
)

func TestZeroCandidatesAreIncompleteAndReportedWithoutNoveltyCall(t *testing.T) {
	openshell.Install(t)
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", source, record.Version); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "ideate", model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	fixture := &ideationFixture{batch: []model.Direction{}}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	err := e.Ideate(ctx, project, "2026-10-22")
	if err == nil || !strings.Contains(err.Error(), "expand, optimize, harden, simplify") {
		t.Fatalf("empty result was treated as a completed ideation run: %v", err)
	}
	if len(fixture.reviewPrompts) != 0 {
		t.Fatalf("empty candidate batch consumed %d semantic review calls", len(fixture.reviewPrompts))
	}
	rows, err := e.Store.List(ctx, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	notified := false
	for _, row := range rows {
		notice, decodeErr := store.Decode[model.Notification](row)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		notified = notified || notice.Kind == "ideation_paused"
	}
	if !notified {
		t.Fatal("empty incomplete run did not notify the human")
	}
}

func TestDiscoveryIncludesPersistedFailuresAndPolicyWithoutExecutingChecks(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	if err := e.Store.Put(ctx, "verification", "failure", map[string]any{"provenance": model.Provenance{Task: task.ID, PolicyHash: project.PolicyHash}, "report": map[string]any{"passed": false, "results": []any{map[string]any{"name": "retry-regression", "passed": false, "output": "pending retry lost after restart"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	prompt, _, _, _, err := e.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "pending retry lost after restart") || !strings.Contains(prompt, project.PolicyHash) {
		t.Fatalf("diagnostics missing: %s", prompt)
	}
}

func TestDiscoverySourceBoundPreservesUTF8(t *testing.T) {
	value := strings.Repeat("a", 23999) + "界" + strings.Repeat("b", 10)
	if result := boundedSources(value); !utf8.ValidString(result) {
		t.Fatal("source excerpt split a UTF8 codepoint")
	}
}

func TestProposalHistoryFindsRejectedMatchOutsideRecentWindow(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	old := model.Proposal{ID: "old-rejected", ProjectID: task.ProjectID, Category: "harden", Problem: "Retired credential refresh path", Mechanism: "Replace expired credential silently", Components: []string{"internal/auth/retired"}, Title: "Old title", Description: "A rejected old proposal", RepositoryRevision: "rev-old", Status: "rejected", Fingerprint: model.ProposalFingerprint(task.ProjectID, "Retired credential refresh path", "Replace expired credential silently", []string{"internal/auth/retired"})}
	if err := e.Store.RecordProposalRejection(ctx, old, "This approach bypasses scoped auth"); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		direction := model.Direction{ID: fmt.Sprintf("recent-%02d", i), ProjectID: task.ProjectID, Category: directionCategories[i%len(directionCategories)], Title: fmt.Sprintf("Recent proposal %02d", i), Description: fmt.Sprintf("Current unrelated proposal %02d", i), Problem: fmt.Sprintf("Recent unrelated problem %02d", i), Mechanism: fmt.Sprintf("Recent unrelated mechanism %02d", i), Components: []string{fmt.Sprintf("internal/recent/%02d", i)}, RepositoryRevision: "rev-current", Status: "shown"}
		if err := e.Store.Put(ctx, "directions", direction.ID, direction, 0); err != nil {
			t.Fatal(err)
		}
	}
	history, err := e.previousDirections(ctx, task.ProjectID, "retired credential refresh path")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range history {
		if item.ID == old.ID && item.Status == "rejected" {
			found = true
		}
	}
	if !found {
		t.Fatalf("old rejected proposal was suppressed outside recent window: %#v", history)
	}
	for _, item := range history {
		if item.ProjectID != task.ProjectID {
			t.Fatalf("cross-project history leaked: %+v", item)
		}
	}
}

func TestProposalHistoryUsesConservativeUTF8ByteBudgetAndRecordLimit(t *testing.T) {
	history := make([]model.Direction, 20)
	for i := range history {
		history[i] = model.Direction{ID: fmt.Sprint(i), Problem: strings.Repeat("界", 80), Mechanism: "mechanism", Components: []string{"internal/workflow"}}
	}
	bounded := boundProposalHistory(history, 4000, 12)
	encoded := jsonText(compactProposalHistory(bounded))
	if len(bounded) > 12 || len(encoded) > 4000 {
		t.Fatalf("history escaped byte/record bound: records=%d bytes=%d", len(bounded), len(encoded))
	}
	if !utf8.ValidString(encoded) {
		t.Fatal("bounded project history is not valid UTF-8")
	}
}

func TestDefaultHistoryAllocationFitsLongMatchAndBoundsTotalRun(t *testing.T) {
	settings := DefaultSettings().Ideation
	history, plans := discoveryHistoryBudgets(settings)
	perCall := history + plans
	attempts := settings.NoveltyRepairAttempts * 2
	if history < 1000 || plans == 0 || perCall*attempts > settings.HistoryRunTokenBudget {
		t.Fatalf("default allocation starves candidate matches or exceeds total run cap: history=%d plans=%d calls=%d run=%d", history, plans, attempts, settings.HistoryRunTokenBudget)
	}
	if settings.HistoryTokenBudget != 4000 || settings.HistoryRunTokenBudget != 12000 {
		t.Fatalf("unexpected default retrieval/run bounds: per_retrieval=%d total_run=%d", settings.HistoryTokenBudget, settings.HistoryRunTokenBudget)
	}
}

func TestFreshDiscoveryExplicitlyReexaminesUnchangedEvidence(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	_, _, snapshot, _, err := e.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.saveDiscoverySnapshot(ctx, project.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	_, _, _, unchanged, err := e.prepareDiscovery(ctx, project)
	if err != nil || !unchanged {
		t.Fatalf("incremental skip=%v %v", unchanged, err)
	}
	prompt, _, _, unchanged, err := e.prepareDiscovery(WithFreshDiscovery(ctx, true), project)
	if err != nil || unchanged || !strings.Contains(prompt, discoveryPolicyFile) {
		t.Fatalf("fresh exploration skipped: %v %v", unchanged, err)
	}
}

func TestConfiguredShortlistLimitRejectsOversizedResult(t *testing.T) {
	batch := ideaBatch(t)
	if err := validateDirections(batch[:3], nil, 2); err == nil {
		t.Fatal("configured shortlist maximum ignored")
	}
	if err := validateDirections(batch[:3], nil, 5); err != nil {
		t.Fatalf("shortlist below configured maximum rejected: %v", err)
	}
}

func TestGeneratedCandidateRequiresBenefitEffortAndHonestUncertainty(t *testing.T) {
	ideas := []model.Direction{{Title: "Retry reliability", Description: "Preserve pending work", Evidence: []model.EvidenceRef{{Kind: "code", Ref: "retry.go"}}}}
	snapshot := discoverySnapshot{Evidence: []model.EvidenceRef{{Kind: "code", Ref: "retry.go", Revision: "revision"}}}
	if err := validateCandidateEvidence(ideas, snapshot); err == nil {
		t.Fatal("generated candidate without benefit, effort or uncertainty accepted")
	}
}

func TestCandidateNeedsProblemMechanismComponentsForStableIdentity(t *testing.T) {
	for _, mutate := range []func(*model.Direction){
		func(d *model.Direction) { d.Problem = "" },
		func(d *model.Direction) { d.Mechanism = "" },
		func(d *model.Direction) { d.Components = nil },
	} {
		candidate := ideaBatch(t)[0]
		candidate.ProjectID = "p"
		mutate(&candidate)
		snapshot := discoverySnapshot{Commit: "current-revision", Evidence: []model.EvidenceRef{{Kind: "code", Ref: ".whirlwind.yaml", Revision: "current-revision"}}}
		if err := validateCandidateEvidence([]model.Direction{candidate}, snapshot); err == nil {
			t.Fatalf("candidate missing problem/mechanism/component identity was accepted: %+v", candidate)
		}
	}
}

func TestCandidateClassificationCannotPresentSourceHypothesisAsObservedDefect(t *testing.T) {
	for _, example := range []struct {
		name, kind, evidence string
		uncertainty          []string
		accepted             bool
	}{
		{"source hypothesis", "observed_defect", "code", nil, false},
		{"check failure", "observed_defect", "verification", nil, true},
		{"honest opportunity", "opportunity", "code", []string{"Validate with representative workload"}, true},
		{"unqualified opportunity", "opportunity", "code", nil, false},
	} {
		t.Run(example.name, func(t *testing.T) {
			ideas := []model.Direction{{ID: "proposal", ProjectID: "p", Title: "Retry reliability", Description: "Preserve pending work", Problem: "Restart loses pending jobs", Mechanism: "Persist recovery checkpoints", Components: []string{"internal/workflow"}, ExpectedBenefit: "Avoid lost jobs", EffortEstimate: "Small scoped change", ProblemKind: example.kind, Uncertainty: example.uncertainty, Evidence: []model.EvidenceRef{{Kind: example.evidence, Ref: "evidence"}}}}
			snapshot := discoverySnapshot{Commit: "revision", Evidence: []model.EvidenceRef{{Kind: example.evidence, Ref: "evidence", Revision: "revision"}}}
			err := validateCandidateEvidence(ideas, snapshot)
			if (err == nil) != example.accepted {
				t.Fatalf("accepted=%v: %v", example.accepted, err)
			}
		})
	}
}

func TestUnselectedLegacyTaskDoesNotRunPlanner(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "tasks", task.ID)
	var legacy map[string]any
	// Preserve the real task shape but remove authorization, as an older database would.
	_ = json.Unmarshal(record.Data, &legacy)
	delete(legacy, "selection")
	legacy["stage"] = "planning"
	if err := e.Store.Put(ctx, "tasks", task.ID, legacy, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := e.Store.Get(ctx, "tasks", task.ID)
	held, _ := store.Decode[model.Task](current)
	if held.Stage != "awaiting_selection" || held.Plan != nil {
		t.Fatalf("legacy task executed: %+v", held)
	}
}

func TestChangedEvidenceCanReopenPreviouslyDismissedProblem(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	_ = e.Store.Put(ctx, "providers", "fake", source, record.Version)
	batch := ideaBatch(t)
	previous := batch[0]
	previous.Status = "dismissed"
	previous.Evidence = []model.EvidenceRef{{Kind: "code", Ref: discoveryPolicyFile, Revision: "old"}}
	fixture := &ideationFixture{batch: batch, reviews: []string{`{"unique":true}`}}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	commit, err := exec.Command("git", "-C", project.Path, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := discoverySnapshot{Commit: strings.TrimSpace(string(commit)), Evidence: []model.EvidenceRef{{Kind: "code", Ref: discoveryPolicyFile, Revision: "new"}}}
	candidates, err := e.generateDirections(ctx, project, "Find useful evidence-backed work", []model.Direction{previous}, snapshot)
	if err != nil || len(candidates) != len(batch) {
		t.Fatalf("changed evidence suppressed: %+v %v", candidates, err)
	}
	for _, candidate := range candidates {
		if candidate.Category == previous.Category {
			if candidate.Evidence[0].Revision != "new" || candidate.RepositoryRevision != strings.TrimSpace(string(commit)) || candidate.Fingerprint == "" || candidate.Status != "proposed" {
				t.Fatalf("reopened candidate was not canonicalized to the pinned snapshot: %+v", candidate)
			}
			return
		}
	}
	t.Fatalf("changed evidence did not reopen dismissed candidate: %+v", candidates)
}
