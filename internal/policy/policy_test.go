package policy

import (
	"testing"

	"khan/internal/model"
)

// TestCommandsRequireExactPrefixAndWorkspacePaths verifies command prefixes must match exact arguments and allowed paths stay inside the workspace.
func TestCommandsRequireExactPrefixAndWorkspacePaths(t *testing.T) {
	p := model.Policy{Commands: [][]string{{"go", "test"}}, AllowedPaths: []string{"src/**", "go.mod"}}
	if !AllowedCommand(p, []string{"go", "test", "./..."}) || AllowedCommand(p, []string{"sh", "-c", "go test"}) {
		t.Fatal("command authorization")
	}
	if !AllowedPath(p, "src/a.go") || AllowedPath(p, "../secret") || AllowedPath(p, ".khan.yaml") {
		t.Fatal("path authorization")
	}
}

// TestPolicyRequiresChecksAndRiskRules verifies project policies without required checks or valid risk rules are rejected.
func TestPolicyRequiresChecksAndRiskRules(t *testing.T) {
	if Validate(model.Policy{}) == nil {
		t.Fatal("empty policy accepted")
	}
	p := model.Policy{Checks: []model.Check{{Name: "tests", Command: []string{"go", "test", "./..."}}}, AllowedPaths: []string{"**"}, RiskRules: []model.RiskRule{{Level: "critical", Paths: []string{"auth/**"}}}}
	if e := Validate(p); e != nil {
		t.Fatal(e)
	}
}
