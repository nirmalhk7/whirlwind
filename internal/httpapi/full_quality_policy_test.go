package httpapi

import (
	"context"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"whirlwind/internal/model"
)

func TestGeneratedPolicyChecksAndSmellsCoverWholeProject(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("Project"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "initial"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	raw, err := generatedExecutionPolicy(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var p model.Policy
	if err := yaml.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	for _, subcommand := range []string{"check", "smells"} {
		found := false
		for _, check := range p.Checks {
			if len(check.Command) > 1 && check.Command[0] == "qlty" && check.Command[1] == subcommand {
				found = true
				if !slices.Contains(check.Command, "--all") {
					t.Fatalf("%s checks only changed files: %v", subcommand, check.Command)
				}
			}
		}
		if !found {
			t.Fatalf("policy omitted qlty %s", subcommand)
		}
	}
}
