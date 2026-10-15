package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"whirlwind/internal/model"
)

// TestCommandsRequireExactPrefixAndWorkspacePaths verifies command prefixes must match exact arguments and allowed paths stay inside the workspace.
func TestCommandsRequireExactPrefixAndWorkspacePaths(t *testing.T) {
	p := model.Policy{Commands: [][]string{{"go", "test"}}, AllowedPaths: []string{"src/**", "go.mod"}}
	if !AllowedCommand(p, []string{"go", "test", "./..."}) || AllowedCommand(p, []string{"sh", "-c", "go test"}) {
		t.Fatal("command authorization")
	}
	if !AllowedPath(p, "src/a.go") || AllowedPath(p, "../secret") || AllowedPath(p, ".whirlwind.yaml") || AllowedPath(model.Policy{AllowedPaths: []string{"**"}}, policyYML) {
		t.Fatal("path authorization")
	}
}

func TestLoadSupportsCommittedYMLAndRejectsLocalEdits(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git("init", "-b", "main")
	path := filepath.Join(repo, policyYML)
	content := []byte("checks:\n  - name: tests\n    command: [go, test, ./...]\nallowed_paths: ['src/**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	git("add", policyYML)
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "-c", "commit.gpgsign=false", "commit", "-m", "Policy")
	p, hash, err := Load(repo)
	if err != nil || hash == "" || len(p.Checks) != 1 || p.Checks[0].Name != "tests" {
		t.Fatalf("committed yml not loaded: %+v %s %v", p, hash, err)
	}
	if err := os.WriteFile(path, append(content, []byte("# local edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(repo); err == nil {
		t.Fatal("local policy edits accepted")
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
