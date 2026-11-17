package verification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const qltyProbeRevParse = "rev-parse"

// Real Qlty file selection is independent of the target repository's language.
// These configured formatter probes certify scope, not semantic scanner health.
func TestParityQltyExactCommandIncludesNewPathsWithoutRemoteUpstream(t *testing.T) {
	formatter, err := exec.LookPath("gofmt")
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct{ name, extension, fileType, script, source string }{
		{"Go", "go", "go", filepath.ToSlash(formatter) + " -w ${target}", "package scope\n\nfunc  %s( ){}\n"},
		{"Python", "py", "python", "/usr/bin/perl -pi -e 's/  / /g' ${target}", "def  %s(): pass\n"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			checkParityQltyLanguageScope(t, probe.extension, probe.fileType, probe.script, probe.source)
		})
	}
}

func checkParityQltyLanguageScope(t *testing.T, extension, fileType, script, source string) {
	t.Helper()
	binary, manifest := parityRunnerFixture(t)
	if _, err := exec.LookPath("qlty"); err != nil {
		t.Fatalf("Qlty scope acceptance requires installed qlty: %v", err)
	}
	tracked, newFile, unchanged := "tracked."+extension, "new."+extension, "unchanged."+extension
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", manifest.Repository}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Scope", "GIT_AUTHOR_EMAIL=scope@example.invalid", "GIT_COMMITTER_NAME=Scope", "GIT_COMMITTER_EMAIL=scope@example.invalid")
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("scope fixture Git: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	for _, name := range []string{tracked, unchanged} {
		if err := os.WriteFile(filepath.Join(manifest.Repository, name), fmt.Appendf(nil, source, "Old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-m", "approved scope base")
	manifest.BaseCommit = git(qltyProbeRevParse, "HEAD")
	if err := os.WriteFile(filepath.Join(manifest.Repository, tracked), fmt.Appendf(nil, source, "Changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Repository, newFile), fmt.Appendf(nil, source, "New"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "saved code version with new and modified source")
	manifest.CandidateCommit, manifest.CandidateTree = git(qltyProbeRevParse, "HEAD"), git(qltyProbeRevParse, "HEAD^{tree}")
	root := t.TempDir()
	manifest.Root = filepath.Join(root, "jobs", manifest.JobID)
	configuration := fmt.Sprintf(`config_version = "0"
[[source]]
name = "default"
default = true
[[plugin]]
name = "scope-probe"
version = "0.10.0"
[plugins.definitions.scope-probe]
file_types = ["%s"]
latest_version = "0.10.0"
known_good_version = "0.10.0"
[plugins.definitions.scope-probe.drivers.format]
script = "%s"
success_codes = [0]
output = "rewrite"
cache_results = false
batch = true
driver_type = "formatter"
suggested = "targets"
`, fileType, script)
	manifest.Quality = &model.QualitySnapshot{Root: root, Files: map[string]string{".qlty/qlty.toml": configuration}}
	argv := []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}
	manifest.Commands = []JobCommand{{Name: "quality", Mode: "autofix", Command: argv, ReportFormat: "sarif", TimeoutSeconds: 15}}
	raw, _ := json.Marshal(manifest)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native Qlty scope job failed: %v %s", err, output)
	}
	assertQltyProbeReport(t, manifest.Root, argv, tracked, newFile, unchanged)
	if git(qltyProbeRevParse, "HEAD") != manifest.CandidateCommit || git("status", "--porcelain", "-uall") != "" {
		t.Fatal("Qlty touched original source repository")
	}
	if _, err := os.Stat(filepath.Join(manifest.Repository, ".qlty")); !os.IsNotExist(err) {
		t.Fatalf("Qlty configuration leaked into original repository: %v", err)
	}
	assertQltyExternalState(t, manifest.Root)
}

func assertQltyProbeReport(t *testing.T, root string, argv []string, tracked, newFile, unchanged string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report JobReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != "fail" || !slices.Equal(report.Results[0].Command, argv) {
		t.Fatalf("Qlty command/scanner outcome unavailable: %s", data)
	}
	output, _, err := ReadJobStreams(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateJobEvidence("sarif", []byte(output), false, false); err != nil {
		t.Fatalf("approved Qlty command did not produce complete structured evidence: %v", err)
	}
	if !strings.Contains(output, tracked) || !strings.Contains(output, newFile) || strings.Contains(output, unchanged) {
		t.Fatalf("Qlty scope missed changed/new files or included unchanged source: %s", output)
	}
}

func assertQltyExternalState(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"logs", "out", "plugin_cachedir", "results"} {
		path := filepath.Join(root, "fixes", "000", "checkout", ".qlty", name)
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		if rel, err := filepath.Rel(canonicalRoot, target); err != nil || !filepath.IsLocal(rel) {
			t.Fatalf("Qlty runtime state escaped approved external root: %s %v", target, err)
		}
	}
}
