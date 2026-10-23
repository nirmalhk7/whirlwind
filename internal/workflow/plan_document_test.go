package workflow

import (
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestPlanDocumentRoundTripPreservesExecutableScope(t *testing.T) {
	plan := model.Plan{Objective: "Keep CI failures visible", What: "Show failed jobs and retry status.", Why: "A completed retry currently hides the original failure.", Benefit: "Developers can identify recurring build failures.", Where: []string{"internal/ci/**", "web/components/Builds.tsx"}, Specification: "Preserve failure timestamps during refresh.", AcceptanceCriteria: []string{"Failed jobs remain visible.", "Retries retain their original failure time."}, Units: []model.Unit{{ID: "backend", Objective: "Write failing timestamp regression test, then retain timestamps.", AllowedPaths: []string{"internal/ci/**"}}, {ID: "frontend", Objective: "Render retry status and test refresh behavior.", AllowedPaths: []string{"web/components/Builds.tsx"}}}}
	document := RenderPlanDocument(plan)
	parsed, err := ParsePlanDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Objective != plan.Objective || parsed.What != plan.What || parsed.Why != plan.Why || parsed.Benefit != plan.Benefit || !reflect.DeepEqual(parsed.Where, plan.Where) || !reflect.DeepEqual(parsed.AcceptanceCriteria, plan.AcceptanceCriteria) {
		t.Fatalf("approved semantics changed: %+v", parsed)
	}
	if !reflect.DeepEqual(parsed.Units, plan.Units) || !strings.Contains(parsed.Specification, "Preserve failure timestamps") {
		t.Fatalf("executable plan lost content or scope: %+v", parsed)
	}
	if next := RenderPlanDocument(parsed); strings.Count(next, "## Specification") != 1 {
		t.Fatalf("render duplicated approved document: %s", next)
	}
}

func TestPlanDocumentManualEditsBecomeAuthoritative(t *testing.T) {
	document := "# Improve retries\n\n## What\n\nExpose retry failures.\n\n## Why\n\nFailures are hidden.\n\n## Benefit\n\nLess debugging time.\n\n## Where\n\n- `internal/ci/retry.go`\n- internal/ci/retry_test.go\n\n## Acceptance criteria\n\n- [ ] Retry failure remains visible.\n\n## Implementation\n\nTest failure first, then update retry rendering.\n"
	edited := strings.ReplaceAll(document, "retry.go", "history.go")
	edited = strings.ReplaceAll(edited, "Retry failure remains visible.", "History retains every failure.")
	parsed, err := ParsePlanDocument(edited)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Where[0] != "internal/ci/history.go" || parsed.Units[0].AllowedPaths[0] != "internal/ci/history.go" || parsed.AcceptanceCriteria[0] != "History retains every failure." || parsed.Specification != strings.TrimSpace(edited) {
		t.Fatalf("manual edits ignored: %+v", parsed)
	}
	withNotes := edited + "\n## Test strategy\n\nExercise retries against a fake server.\n"
	withNotesPlan, err := ParsePlanDocument(withNotes)
	if err != nil {
		t.Fatal(err)
	}
	if rendered := RenderPlanDocument(withNotesPlan); rendered != withNotes {
		t.Fatalf("rerender lost manually added review notes: %s", rendered)
	}
	for _, invalid := range []string{
		strings.Replace(document, "## Why\n\nFailures are hidden.", "", 1),
		strings.Replace(document, "- [ ] Retry failure remains visible.", "Retry failure remains visible.", 1),
		strings.ReplaceAll(document, "internal/ci/retry.go", "../secrets"),
		strings.ReplaceAll(document, "internal/ci/retry.go", "/etc/passwd"),
		strings.ReplaceAll(document, "internal/ci/retry.go", "src/../../outside"),
		strings.ReplaceAll(document, "internal/ci/retry.go", "C:\\outside"),
		document + "\n## Where\n\n- other.go\n",
	} {
		if _, err := ParsePlanDocument(invalid); err == nil {
			t.Errorf("accepted invalid plan: %s", invalid)
		}
	}
}

func TestPlanDocumentLegacyPlanUsesExplicitFallbacks(t *testing.T) {
	plan := model.Plan{Objective: "Fix retry history", Specification: "Retries overwrite failures.", AcceptanceCriteria: []string{"History retains failures."}, Units: []model.Unit{{ID: "first", Objective: "Add regression test.", AllowedPaths: []string{"src/**"}}, {ID: "second", Objective: "Retain failure.", AllowedPaths: []string{"src/**", "tests/**"}}}}
	parsed, err := ParsePlanDocument(RenderPlanDocument(plan))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.What != "Fix retry history" || parsed.Why != "Retries overwrite failures." || parsed.Benefit != "History retains failures." || !reflect.DeepEqual(parsed.Where, []string{"src/**", "tests/**"}) {
		t.Fatalf("legacy fallback lost intent: %+v", parsed)
	}
	plan.Units = nil
	if _, err := ParsePlanDocument(RenderPlanDocument(plan)); err == nil {
		t.Fatal("plan without concrete scope became executable")
	}
}

func TestPlanDocumentKeepsScopedUnitsAndDependencies(t *testing.T) {
	plan := model.Plan{Objective: "Improve result", What: "Fix result", Why: "Wrong result", Benefit: "Correct result", Where: []string{"a.go", "a_test.go", "b.go", "b_test.go"}, AcceptanceCriteria: []string{"Correct"}, Units: []model.Unit{{ID: "first", Objective: "Implement first regression", AllowedPaths: []string{"a.go", "a_test.go"}}, {ID: "second", Objective: "Implement dependent regression", Dependencies: []string{"first"}, AllowedPaths: []string{"b.go", "b_test.go"}}}}
	parsed, err := ParsePlanDocument(RenderPlanDocument(plan))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Units, plan.Units) {
		t.Fatalf("approved unit scopes changed: %+v", parsed.Units)
	}
}

func TestPlanDocumentUnitsCanNarrowComponentScope(t *testing.T) {
	plan := model.Plan{Objective: "Fix CI", What: "Keep CI failures", Why: "Missing failure", Benefit: "Correct CI", Where: []string{"internal/ci/**"}, AcceptanceCriteria: []string{"Failure visible"}, Units: []model.Unit{{ID: "ci", Objective: "Fix and test", AllowedPaths: []string{"internal/ci/run.go", "internal/ci/run_test.go"}}}}
	parsed, err := ParsePlanDocument(RenderPlanDocument(plan))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Units, plan.Units) {
		t.Fatalf("unit scope expanded: %+v", parsed.Units)
	}
}
