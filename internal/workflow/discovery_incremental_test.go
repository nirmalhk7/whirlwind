package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestZeroCandidatesCompletesDiscoveryWithoutNoveltyCall(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	_ = e.Store.Put(ctx, "providers", "fake", source, record.Version)
	fixture := &ideationFixture{batch: []model.Direction{}}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	if err := e.Ideate(ctx, project, "2026-10-22"); err != nil {
		t.Fatalf("empty useful result failed: %v", err)
	}
	if fixture.calls != 1 {
		t.Fatalf("empty result consumed %d calls", fixture.calls)
	}
	if err := e.Ideate(ctx, project, "2026-10-23"); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != 1 {
		t.Fatalf("unchanged evidence consumed %d calls", fixture.calls)
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
	if err != nil || unchanged || !strings.Contains(prompt, ".whirlwind.yaml") {
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
			ideas := []model.Direction{{Title: "Retry reliability", Description: "Preserve pending work", ExpectedBenefit: "Avoid lost jobs", EffortEstimate: "Small scoped change", ProblemKind: example.kind, Uncertainty: example.uncertainty, Evidence: []model.EvidenceRef{{Kind: example.evidence, Ref: "evidence"}}}}
			snapshot := discoverySnapshot{Evidence: []model.EvidenceRef{{Kind: example.evidence, Ref: "evidence", Revision: "revision"}}}
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
	batch := ideaBatch(t)[:1]
	previous := batch[0]
	previous.Status = "dismissed"
	previous.Evidence = []model.EvidenceRef{{Kind: "code", Ref: ".whirlwind.yaml", Revision: "old"}}
	fixture := &ideationFixture{batch: batch}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	snapshot := discoverySnapshot{Evidence: []model.EvidenceRef{{Kind: "code", Ref: ".whirlwind.yaml", Revision: "new"}}}
	candidates, err := e.generateDirections(ctx, project, "Find useful evidence-backed work", []model.Direction{previous}, snapshot)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("changed evidence suppressed: %+v %v", candidates, err)
	}
	if candidates[0].Evidence[0].Revision != "new" {
		t.Fatalf("uncanonical evidence: %+v", candidates[0].Evidence)
	}
}
