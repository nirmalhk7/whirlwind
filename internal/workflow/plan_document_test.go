package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	planInternalCIPath  = "internal/ci/**"
	planRetrySourcePath = "internal/ci/retry.go"
	planSourceGlob      = "src/**"
)

func TestPlanDocumentRoundTripPreservesExecutableScope(t *testing.T) {
	plan := model.Plan{Objective: "Keep CI failures visible", What: "Show failed jobs and retry status.", Why: "A completed retry currently hides the original failure.", Benefit: "Developers can identify recurring build failures.", Where: []string{planInternalCIPath, "web/components/Builds.tsx"}, Scope: []string{"Retain original failed job state.", "Render the retry timestamp."}, Exclusions: []string{"Do not alter CI provider APIs."}, Steps: []string{"Add retry-history regression tests.", "Preserve and display original failure data."}, RepositoryTestInventory: "Configured: go test ./... from .github/workflows/test.yml.", RepositoryBuildInventory: "Configured: go build ./... from .github/workflows/test.yml.", RepositoryTestCommands: [][]string{{"go", "test", "./..."}}, RepositoryBuildCommands: [][]string{{"go", "build", "./..."}}, Specification: "Preserve failure timestamps during refresh.", AcceptanceCriteria: []string{"Failed jobs remain visible.", "Retries retain their original failure time."}, Units: []model.Unit{{ID: "backend", Objective: "Write failing timestamp regression test, then retain timestamps.", AllowedPaths: []string{planInternalCIPath}}, {ID: "frontend", Objective: "Render retry status and test refresh behavior.", AllowedPaths: []string{"web/components/Builds.tsx"}}}}
	document := RenderPlanDocument(plan)
	parsed, err := ParsePlanDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Objective != plan.Objective || parsed.What != plan.What || parsed.Why != plan.Why || parsed.Benefit != plan.Benefit || !reflect.DeepEqual(parsed.Where, plan.Where) || !reflect.DeepEqual(parsed.Scope, plan.Scope) || !reflect.DeepEqual(parsed.Exclusions, plan.Exclusions) || !reflect.DeepEqual(parsed.Steps, plan.Steps) || parsed.RepositoryTestInventory != plan.RepositoryTestInventory || parsed.RepositoryBuildInventory != plan.RepositoryBuildInventory || !reflect.DeepEqual(parsed.AcceptanceCriteria, plan.AcceptanceCriteria) {
		t.Fatalf("approved semantics changed: %+v", parsed)
	}
	if !reflect.DeepEqual(parsed.Units, plan.Units) || !strings.Contains(parsed.Specification, "Preserve failure timestamps") {
		t.Fatalf("executable plan lost content or scope: %+v", parsed)
	}
	if next := RenderPlanDocument(parsed); strings.Count(next, "## Specification") != 1 {
		t.Fatalf("render duplicated approved document: %s", next)
	}
}

func TestPlanDocumentPreservesRequirementsAndVerificationCommands(t *testing.T) {
	var plan model.Plan
	raw := []byte(`{"objective":"Fix retries","what":"Preserve failures","why":"Retries overwrite evidence","benefit":"Failures remain visible","where":["internal/retry.go"],"acceptance_criteria":["Old failure remains visible"],"units":[{"id":"main","objective":"Add regression test and preserve history","allowed_paths":["internal/retry.go"]}],"requirements":[{"id":"REQ-001","outcome":"A retry never removes the original failure record.","evidence_method":"Run TestRetryRetainsOriginalFailure"}],"checks":[{"id":"check-001","name":"retry regression","command":["go","test","./internal/retry"]}],"repository_test_commands":[["go","test","./..."]],"repository_build_commands":[["go","build","./..."]]}`)
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	document := RenderPlanDocument(plan)
	for _, want := range []string{"REQ-001", "A retry never removes the original failure record.", "Run TestRetryRetainsOriginalFailure", "`go test ./internal/retry`", "`go test ./...`", "`go build ./...`"} {
		if !strings.Contains(document, want) {
			t.Errorf("approved plan document omitted %q: %s", want, document)
		}
	}
	parsed, err := ParsePlanDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requirements", "checks", "repository_test_commands", "repository_build_commands"} {
		values, ok := got[key].([]any)
		if !ok || len(values) != 1 {
			t.Errorf("parsed plan dropped %s: %s", key, encoded)
		}
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
		strings.ReplaceAll(document, planRetrySourcePath, "../secrets"),
		strings.ReplaceAll(document, planRetrySourcePath, "/etc/passwd"),
		strings.ReplaceAll(document, planRetrySourcePath, "src/../../outside"),
		strings.ReplaceAll(document, planRetrySourcePath, "C:\\outside"),
		document + "\n## Where\n\n- other.go\n",
	} {
		if _, err := ParsePlanDocument(invalid); err == nil {
			t.Errorf("accepted invalid plan: %s", invalid)
		}
	}
}

func TestPlanDocumentLegacyPlanUsesExplicitFallbacks(t *testing.T) {
	plan := model.Plan{Objective: "Fix retry history", Specification: "Retries overwrite failures.", AcceptanceCriteria: []string{"History retains failures."}, Units: []model.Unit{{ID: "first", Objective: "Add regression test.", AllowedPaths: []string{planSourceGlob}}, {ID: "second", Objective: "Retain failure.", AllowedPaths: []string{planSourceGlob, "tests/**"}}}}
	parsed, err := ParsePlanDocument(RenderPlanDocument(plan))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.What != "Fix retry history" || parsed.Why != "Retries overwrite failures." || parsed.Benefit != "History retains failures." || !reflect.DeepEqual(parsed.Where, []string{planSourceGlob, "tests/**"}) {
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
	plan := model.Plan{Objective: "Fix CI", What: "Keep CI failures", Why: "Missing failure", Benefit: "Correct CI", Where: []string{planInternalCIPath}, AcceptanceCriteria: []string{"Failure visible"}, Units: []model.Unit{{ID: "ci", Objective: "Fix and test", AllowedPaths: []string{"internal/ci/run.go", "internal/ci/run_test.go"}}}}
	parsed, err := ParsePlanDocument(RenderPlanDocument(plan))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Units, plan.Units) {
		t.Fatalf("unit scope expanded: %+v", parsed.Units)
	}
}
