package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

const (
	externalQltyConfigFilename = "qlty.toml"
	externalQltyEmptyConfig    = "config_version = \"0\"\n"
)

func configureExternalQualityFixture(t *testing.T, e *Engine, plan model.ExecutionPlan, root string, configuredFiles ...[]string) {
	t.Helper()
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(project.Policy)
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	files := []string{".qlty/qlty.toml"}
	if len(configuredFiles) > 0 {
		files = configuredFiles[0]
	}
	values["quality"] = map[string]any{"root": root, "configuration_files": files}
	raw, err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, ".whirlwind.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, project.Path, "git", "add", ".whirlwind.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, project.Path, "git", "commit", "-m", "human approved external quality configuration"); err != nil {
		t.Fatal(err)
	}
	project.Policy, project.PolicyHash, err = policy.Load(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "projects", project.ID, project, r.Version); err != nil {
		t.Fatal(err)
	}
}

func TestParityApprovalFreezesExternalQualityConfiguration(t *testing.T) {
	e, plan := pendingCodingPairFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	config := filepath.Join(root, "config", ".qlty", externalQltyConfigFilename)
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(externalQltyEmptyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	configureExternalQualityFixture(t, e, plan, root)
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	var frozen json.RawMessage
	for _, reference := range saved.Plan.Approval.References {
		if reference.Kind == "quality_files" {
			frozen = reference.Data
		}
	}
	if len(frozen) == 0 || !strings.Contains(string(frozen), "config_version") {
		t.Fatal("approval omitted exact external configuration bytes")
	}
	if err := os.WriteFile(config, []byte("config_version = \"changed\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, plan.ID); err == nil {
		t.Fatal("external configuration drift launched execution without new approval")
	}
	after, _ := localStoredTask(t, e, plan.ID)
	if after.Stage != "plan_approval" {
		t.Fatalf("configuration drift not returned for approval: %s", after.Stage)
	}
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, ".qlty")); !os.IsNotExist(err) {
		t.Fatal("approval wrote Qlty configuration into source repository")
	}
}

func TestParityQualityApprovalRejectsMissingConfigurationAndRepositoryRoot(t *testing.T) {
	for _, scenario := range []string{"missing", "inside repository", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			e, plan := pendingCodingPairFixture(t)
			ctx := context.Background()
			root := t.TempDir()
			if scenario == "inside repository" {
				project, _ := e.Project(ctx, plan.ProjectID)
				root = filepath.Join(project.Path, "quality")
			}
			if scenario == "symlink" {
				config := filepath.Join(root, "config", ".qlty", externalQltyConfigFilename)
				requireWorkflowSuccess(t, os.MkdirAll(filepath.Dir(config), 0700))
				requireWorkflowSuccess(t, os.Symlink("/etc/hosts", config))
			}
			configureExternalQualityFixture(t, e, plan, root)
			_, version := localStoredTask(t, e, plan.ID)
			if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err == nil {
				t.Fatalf("unsafe external quality setup approved: %s", scenario)
			}
		})
	}
}

func TestParitySupervisorStagesQualityJobsUnderApprovedExternalRoot(t *testing.T) {
	e, plan := pendingCodingPairFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	config := filepath.Join(root, "config", ".qlty", externalQltyConfigFilename)
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(externalQltyEmptyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	configureExternalQualityFixture(t, e, plan, root)
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	plan, _ = localStoredTask(t, e, plan.ID)
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Worktree = project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	installNativeParityVerifier(t)
	run, err := e.startVerification(ctx, project, &plan)
	if err != nil {
		t.Fatal(err)
	}
	// The configured result-file assertion may fail normally; external setup
	// itself must finish and retain the same complete command evidence.
	report, err := run.runLocalChecks(project.Policy, project.Policy.Checks, nil)
	if err != nil || len(report.Results) != 1 {
		t.Fatalf("quality staging setup failed: %+v %v", report, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "jobs", run.id, "manifest.json"))
	if err != nil {
		t.Fatalf("supervisor ignored approved external storage root: %v", err)
	}
	if !strings.Contains(string(raw), "config_version") {
		t.Fatal("supervisor omitted frozen quality configuration from manifest")
	}
	copied, err := os.ReadFile(filepath.Join(root, "jobs", run.id, "checkout", ".qlty", externalQltyConfigFilename))
	if err != nil || string(copied) != externalQltyEmptyConfig {
		t.Fatalf("frozen config not copied to disposable checkout: %q %v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, ".qlty")); !os.IsNotExist(err) {
		t.Fatal("quality job changed original repository")
	}
}

func TestParityQualityConfigurationUsesDefaultExternalRoot(t *testing.T) {
	e, plan := pendingCodingPairFixture(t)
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	expected := filepath.Join(home, ".whirlwind", "projects", plan.ProjectID, "qlty")
	config := filepath.Join(expected, "config", ".qlty", externalQltyConfigFilename)
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(externalQltyEmptyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	configureExternalQualityFixture(t, e, plan, "")
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	expected, err := filepath.EvalSymlinks(expected)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range saved.Plan.Approval.References {
		if reference.Kind != "quality_files" {
			continue
		}
		var snapshot model.QualitySnapshot
		if err := json.Unmarshal(reference.Data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Root != expected {
			t.Fatalf("default external root wrong: %s", snapshot.Root)
		}
		return
	}
	t.Fatal("default external configuration not frozen")
}
