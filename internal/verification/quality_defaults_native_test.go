package verification

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	defaultQualityFixtureCatalogCheck = "effective-catalog"
)

func TestPreparedDefaultConfigurationRunsActualGitleaks(t *testing.T) {
	if _, err := exec.LookPath("qlty"); err != nil {
		t.Fatalf("installed Qlty required for default qualification: %v", err)
	}
	binary, base := parityRunnerFixture(t)
	manifest := projectSetupFixture(t, base, nil)
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(filepath.Join(root, "jobs"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	manifest.Root = filepath.Join(root, "jobs", manifest.JobID)
	manifest.Quality = &model.QualitySnapshot{Root: root, Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n[[source]]\nname = \"default\"\ndefault = true\n"}}
	manifest.Commands = []JobCommand{{Name: "catalog", Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 30}}
	report, err := RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil || !report.Passed {
		t.Fatalf("native catalog discovery failed: %+v %v", report, err)
	}
	catalog, _, err := ReadJobStreams(manifest.Root, 0)
	requireVerificationSuccess(t, err)
	snapshot, err := manifest.PrepareQualityDefaults([]byte(catalog))
	requireVerificationSuccess(t, err)
	manifest.JobID, manifest.OperationID = defaultQualityFixtureCatalogCheck, defaultQualityFixtureCatalogCheck
	manifest.Root = filepath.Join(root, "jobs", manifest.JobID)
	manifest.Quality = &snapshot
	manifest.Commands = []JobCommand{{Name: defaultQualityFixtureCatalogCheck, Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 30}}
	report, err = RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil || !report.Passed {
		t.Fatalf("effective catalog discovery failed: %+v %v", report, err)
	}
	effectiveCatalog, _, err := ReadJobStreams(manifest.Root, 0)
	requireVerificationSuccess(t, err)
	snapshot.PluginCatalog = []byte(effectiveCatalog)
	writeNativeDefaultProbe(t, &manifest)
	manifest.JobID, manifest.OperationID = "qualification", "qualification"
	manifest.Root = filepath.Join(root, "jobs", manifest.JobID)
	manifest.Quality = &snapshot
	// Installed Qlty is not a preinstalled plugin runtime. This fixture grants
	// only public distribution hosts, through the existing enforced proxy.
	manifest.Sandbox = &model.SandboxPolicy{AllowedDomains: []string{"github.com", "api.github.com", "release-assets.githubusercontent.com"}}
	argv := []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}
	// Cold provisioning includes plugin installation. Keep it bounded
	// by the existing generic-command default, independently of ACP deadlines.
	manifest.Commands = []JobCommand{{Name: "quality", Mode: "autofix", Command: argv, ReportFormat: "sarif", TimeoutSeconds: 600}}
	report, err = RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil || report.Passed || len(report.Results) != 1 || report.Results[0].Status != "fail" {
		t.Fatalf("default scanners did not report intentional canaries: %+v %v", report, err)
	}
	output, _, err := ReadJobStreams(manifest.Root, 0)
	requireVerificationSuccess(t, err)
	assertNativeDefaultCanary(t, output)
	qualifiedSnapshot := snapshot
	qualifiedSnapshot.Identity = report.QualityIdentity
	manifest.JobID, manifest.OperationID = "candidate", "candidate"
	manifest.Root = filepath.Join(root, "jobs", manifest.JobID)
	manifest.Quality = &qualifiedSnapshot
	candidateReport, candidateErr := RunLocalJob(context.Background(), binary, manifest, nil)
	if candidateErr != nil || candidateReport.QualityIdentity == nil || !model.SameQualityIdentity(report.QualityIdentity, candidateReport.QualityIdentity) {
		t.Fatalf("candidate did not reuse qualified runtime identity: %+v %v", candidateReport, candidateErr)
	}
	candidateOutput, _, err := ReadJobStreams(manifest.Root, 0)
	requireVerificationSuccess(t, err)
	assertNativeDefaultCanary(t, candidateOutput)
	if _, err := os.Lstat(filepath.Join(manifest.Repository, ".qlty")); !os.IsNotExist(err) {
		t.Fatalf("default scanner preparation polluted original source: %v", err)
	}
	if output := nativeDefaultGit(t, manifest.Repository, "status", "--porcelain"); output != "" {
		t.Fatalf("default scanners changed original source: %s", output)
	}
}

func writeNativeDefaultProbe(t *testing.T, manifest *JobManifest) {
	t.Helper()
	content := "token = \"" + "gh" + "p_" + strings.Repeat("aB3cD4eF5gH6", 3) + "\"\n"
	if err := os.WriteFile(filepath.Join(manifest.Repository, "probe.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	nativeDefaultGit(t, manifest.Repository, "add", ".")
	nativeDefaultGit(t, manifest.Repository, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-m", "Harmless native scanner qualification canaries")
	manifest.CandidateCommit = nativeDefaultGit(t, manifest.Repository, "rev-parse", "HEAD")
	manifest.CandidateTree = nativeDefaultGit(t, manifest.Repository, "rev-parse", "HEAD^{tree}")
}

func nativeDefaultGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Qualification", "GIT_AUTHOR_EMAIL=qualification@localhost", "GIT_COMMITTER_NAME=Qualification", "GIT_COMMITTER_EMAIL=qualification@localhost")
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native qualification Git: %v %s", err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func assertNativeDefaultCanary(t *testing.T, raw string) {
	t.Helper()
	var document struct {
		Runs []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					Physical struct {
						Artifact struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	for _, run := range document.Runs {
		for _, result := range run.Results {
			for _, location := range result.Locations {
				if strings.HasPrefix(result.RuleID, "gitleaks:") && location.Physical.Artifact.URI == "probe.txt" {
					return
				}
			}
		}
	}
	t.Fatalf("default Gitleaks rules did not detect the canary: %s", raw)
}
