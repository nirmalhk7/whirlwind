package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const defaultSnapshotSecretPath = ".gitleaks.toml"

func TestDefaultQualityApprovalFreezesLocalRuleAndSecretConfiguration(t *testing.T) {
	for _, changed := range []string{".semgrep/ci.yaml", defaultSnapshotSecretPath} {
		t.Run(changed, func(t *testing.T) {
			assertDefaultQualityApprovalDrift(t, changed)
		})
	}
}

func assertDefaultQualityApprovalDrift(t *testing.T, changed string) {
	t.Helper()
	e, plan := pendingCodingPairFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	files := writeDefaultQualitySnapshotFixture(t, root)
	configureExternalQualityFixture(t, e, plan, root, nil)
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	snapshot, err := approvedQualitySnapshot(saved)
	if err != nil || snapshot == nil || len(snapshot.Files) != 3 {
		t.Fatalf("default approval omitted local scanner policy: %+v %v", snapshot, err)
	}
	for path, content := range files {
		if snapshot.Files[path] != content {
			t.Fatalf("approval lost exact bytes for %s", path)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config", changed), []byte(files[changed]+"\n# Changed externally\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stopped := false
	e.running[plan.ID] = func() { stopped = true }
	if err := e.ValidateLocalPlan(ctx, saved); err == nil {
		t.Fatal("changed local scanner rules retained approval")
	}
	after, _ := localStoredTask(t, e, plan.ID)
	if after.Stage != "plan_approval" || !stopped {
		t.Fatalf("scanner drift did not stop old work: stage=%s stopped=%v", after.Stage, stopped)
	}
}

func TestDefaultQualityApprovalRejectsLinkedScannerConfiguration(t *testing.T) {
	e, plan := pendingCodingPairFixture(t)
	root := t.TempDir()
	writeDefaultQualitySnapshotFixture(t, root)
	path := filepath.Join(root, "config", defaultSnapshotSecretPath)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", path); err != nil {
		t.Fatal(err)
	}
	configureExternalQualityFixture(t, e, plan, root, nil)
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(context.Background(), plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err == nil {
		t.Fatal("default approval followed or ignored linked scanner configuration")
	}
}

func writeDefaultQualitySnapshotFixture(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{
		".qlty/qlty.toml":         externalQltyEmptyConfig,
		defaultSnapshotSecretPath: "[extend]\nuseDefault = true\n",
		".semgrep/ci.yaml":        "rules:\n- id: original\n  pattern: print(...)\n  languages: [python]\n  severity: WARNING\n  message: Local rule\n",
	}
	for path, content := range files {
		path = filepath.Join(root, "config", path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}
