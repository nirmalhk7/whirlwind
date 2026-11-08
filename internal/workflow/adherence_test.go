package workflow

import (
	"fmt"
	"math"
	"testing"
	"whirlwind/internal/model"
)

func adherenceFixture(total, passed int) ([]model.Requirement, []model.RequirementResult, model.Provenance) {
	requirements := make([]model.Requirement, total)
	results := make([]model.RequirementResult, total)
	for i := range total {
		id := fmt.Sprintf("R-%d", i+1)
		requirements[i] = model.Requirement{ID: id, Outcome: "specified outcome", EvidenceMethod: "configured behavior test"}
		status := "fail"
		if i < passed {
			status = "pass"
		}
		results[i] = model.RequirementResult{ID: id, Status: status, Code: []string{"result.go:10"}, Evidence: []string{"test-log:verified"}, Explanation: "Observed behavior matches judgment"}
	}
	return requirements, results, model.Provenance{Task: "approved-plan", PlanVersion: 2, Head: "reviewed-snapshot", Base: "pinned-base", PolicyHash: "policy", VerificationID: "checks"}
}

func TestAdherenceUsesStrictUnroundedEightyPercent(t *testing.T) {
	for _, tc := range []struct {
		total, passed int
		accepted      bool
		score         float64
	}{{5, 4, false, 80}, {6, 5, true, 83.33333333333333}, {1, 1, true, 100}, {5, 3, false, 60}} {
		requirements, results, proof := adherenceFixture(tc.total, tc.passed)
		report, err := EvaluateAdherence(requirements, results, proof, proof, true)
		if err != nil {
			t.Fatal(err)
		}
		if report.Accepted != tc.accepted || math.Abs(report.Score-tc.score) > 0.0000001 || report.Passed != tc.passed || report.Total != tc.total {
			t.Errorf("%d/%d: got %+v", tc.passed, tc.total, report)
		}
	}
}

func TestAdherenceKeepsMissingAndPartialOutcomesUnverified(t *testing.T) {
	requirements, results, proof := adherenceFixture(6, 6)
	results = results[:5]
	results[0].Evidence = nil
	report, err := EvaluateAdherence(requirements, results, proof, proof, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 4 || report.Accepted || len(report.Results) != 6 {
		t.Fatalf("missing/unsupported outcomes earned credit: %+v", report)
	}
	if report.Results[0].Status != "unverified" || report.Results[5].Status != "unverified" {
		t.Fatalf("unmet requirements hidden: %+v", report.Results)
	}
}

func TestAdherenceFailedGateOverridesPerfectScore(t *testing.T) {
	requirements, results, proof := adherenceFixture(1, 1)
	report, err := EvaluateAdherence(requirements, results, proof, proof, false)
	if err != nil || report.Accepted || report.Score != 100 {
		t.Fatalf("mandatory gate bypassed: %+v %v", report, err)
	}
}

func TestAdherenceRejectsChangedSnapshotOrApprovedVersion(t *testing.T) {
	requirements, results, proof := adherenceFixture(1, 1)
	for _, field := range []string{"head", "version", "plan", "base", "policy", "verification"} {
		actual := proof
		switch field {
		case "head":
			actual.Head = "other"
		case "version":
			actual.PlanVersion++
		case "plan":
			actual.Task = "other"
		case "base":
			actual.Base = "other"
		case "policy":
			actual.PolicyHash = "other"
		case "verification":
			actual.VerificationID = "other"
		}
		if _, err := EvaluateAdherence(requirements, results, proof, actual, true); err == nil {
			t.Errorf("accepted stale %s evidence", field)
		}
	}
}

func TestAdherenceRejectsInvalidOrReweightedRequirementLists(t *testing.T) {
	requirements, results, proof := adherenceFixture(2, 2)
	for _, tc := range []struct {
		name         string
		requirements []model.Requirement
		results      []model.RequirementResult
	}{
		{"empty", nil, nil},
		{"duplicate approved ID", []model.Requirement{requirements[0], requirements[0]}, results},
		{"duplicate judgment", requirements, []model.RequirementResult{results[0], results[0]}},
		{"unknown judgment", requirements[:1], results},
		{"invalid judgment", requirements, []model.RequirementResult{{ID: "R-1", Status: "partial"}}},
	} {
		if _, err := EvaluateAdherence(tc.requirements, tc.results, proof, proof, true); err == nil {
			t.Errorf("accepted %s", tc.name)
		}
	}
}
