package verification

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	qualityFixtureGitleaksPath   = ".gitleaks.toml"
	qualityFixtureQltyPath       = ".qlty/qlty.toml"
	qualityFixtureScannerMarker  = "1.2.3-abcdef012345.done"
	qualityFixtureScannerPackage = "1.2.3-abcdef012345"
)

func TestCaptureQualityIdentityBindsInstalledRuntimeAndRules(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	qlty := filepath.Join(bin, "qlty")
	if err := os.WriteFile(qlty, []byte("#!/bin/sh\n# qlty binary fixture\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runtimeDir := filepath.Join(root, "runtime")
	toolRoot := filepath.Join(runtimeDir, "home", ".qlty", "cache", "tools")
	packageDir := filepath.Join(toolRoot, "gitleaks", qualityFixtureScannerPackage)
	if err := os.MkdirAll(filepath.Join(packageDir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "bin", "gitleaks"), []byte("scanner one"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolRoot, "gitleaks", qualityFixtureScannerMarker), nil, 0600); err != nil {
		t.Fatal(err)
	}
	config := model.QualitySnapshot{Root: filepath.Join(root, "quality"), Files: map[string]string{
		qualityFixtureQltyPath:     "[[plugin]]\nname = \"gitleaks\"\nversion = \"1.2.3\"\nscript = \"gitleaks --config .gitleaks.toml\"\n",
		qualityFixtureGitleaksPath: "[extend]\nuseDefault = true\n",
	}, PluginCatalog: []byte("plugins:\n  gitleaks:\n    version: 1.2.3\n")}
	output := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","semanticVersion":"0.668.0","version":"0.668.0 macos-arm64 (build)"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`
	first, err := captureQualityIdentity(output, runtimeDir, qlty, &config, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.QltyVersion != "0.668.0 macos-arm64 (build)" || len(first.Tools) != 1 || first.Tools[0] != (model.QualityToolIdentity{Name: "gitleaks", Version: "1.2.3", Fingerprint: "abcdef012345"}) {
		t.Fatalf("actual runtime identity missing versioned scanner package: %+v", first)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "bin", "gitleaks"), []byte("scanner two"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := captureQualityIdentity(output, runtimeDir, qlty, &config, "")
	if err != nil {
		t.Fatal(err)
	}
	if model.SameQualityIdentity(first, second) {
		t.Fatal("changed scanner runtime retained the frozen identity")
	}
	config.Files[qualityFixtureGitleaksPath] += "# changed rules\n"
	third, err := captureQualityIdentity(output, runtimeDir, qlty, &config, "")
	if err != nil {
		t.Fatal(err)
	}
	if model.SameQualityIdentity(second, third) {
		t.Fatal("changed rule bytes retained the frozen identity")
	}
}

func TestCaptureQualityIdentityFailsClosedForRemoteRulesAndMissingVersion(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	qlty := filepath.Join(bin, "qlty")
	if err := os.WriteFile(qlty, []byte("qlty"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runtimeDir := filepath.Join(root, "runtime")
	toolRoot := filepath.Join(runtimeDir, "home", ".qlty", "cache", "tools", "gitleaks")
	packageDir := filepath.Join(toolRoot, qualityFixtureScannerPackage)
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolRoot, qualityFixtureScannerMarker), nil, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := &model.QualitySnapshot{Root: filepath.Join(root, "quality"), Files: map[string]string{qualityFixtureQltyPath: "config_version = \"0\"\n", qualityFixtureGitleaksPath: "[extend]\nuseDefault = true\n"}, PluginCatalog: []byte("plugins:\n  gitleaks:\n    version: 1.2.3\n")}
	output := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","semanticVersion":"0.668.0","version":"0.668.0"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`
	if _, err := captureQualityIdentity(output, runtimeDir, qlty, snapshot, ""); err == nil {
		t.Fatal("empty scanner runtime package was accepted")
	}
	if err := os.WriteFile(filepath.Join(packageDir, "gitleaks"), []byte("scanner"), 0700); err != nil {
		t.Fatal(err)
	}
	snapshot.Files[qualityFixtureQltyPath] = "script = \"gitleaks --config https://example.invalid/rules.toml\"\n"
	if _, err := captureQualityIdentity(output, runtimeDir, qlty, snapshot, ""); err == nil || !strings.Contains(err.Error(), "remote rule") {
		t.Fatalf("remote Qlty policy did not fail closed: %v", err)
	}
	snapshot.Files[qualityFixtureQltyPath] = "config_version = \"0\"\n"
	if _, err := captureQualityIdentity(strings.Replace(output, `,"version":"0.668.0"`, "", 1), runtimeDir, qlty, snapshot, ""); err == nil {
		t.Fatal("Qlty SARIF without the full tool version was accepted")
	}
}

func TestHashQualityTreeUsesUnambiguousRecordsAndIncludesLockFiles(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "a"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "b"), []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(right, "a"), []byte("x\x00b\x000\x00600\x00Fy\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	leftHash, err := hashQualityTree(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := hashQualityTree(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash == rightHash {
		t.Fatal("different file trees produced the same framed identity")
	}
	if err := os.WriteFile(filepath.Join(left, "scanner.lock"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := hashQualityTree(left)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "scanner.lock"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := hashQualityTree(left)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("scanner package lock file mutation was omitted from identity")
	}
}

func TestHashQltyPluginCatalogUsesCompleteCanonicalDefinitions(t *testing.T) {
	one := []byte("plugins:\n  trivy:\n    version: 1.2.3\n    source: local\n  gitleaks:\n    version: 8.0.0\n")
	two := []byte("plugins:\n  gitleaks:\n    version: 8.0.0\n  trivy:\n    source: local\n    version: 1.2.3\n")
	a, err := HashQltyPluginCatalog(one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashQltyPluginCatalog(two)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("equivalent YAML map ordering changed effective catalog identity")
	}
	changed, err := HashQltyPluginCatalog([]byte("plugins:\n  trivy:\n    version: 1.2.3\n    source: remote\n  gitleaks:\n    version: 8.0.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a == changed {
		t.Fatal("changed plugin source retained the same catalog identity")
	}
}

func TestFrozenAstGrepConfigRejectsRemoteRuleSources(t *testing.T) {
	files := map[string]string{"sgconfig.yml": "$schema: https://example.invalid/schema.json\nruleDirs: [rules]\n"}
	if err := validateFrozenRuleSources(files); err != nil {
		t.Fatalf("local rule configuration was rejected: %v", err)
	}
	files["sgconfig.yml"] = "include: https://example.invalid/rules.yml\n"
	if err := validateFrozenRuleSources(files); err == nil {
		t.Fatal("remote rule source was accepted")
	}
}

func TestQltyBinaryHashUsesCommandEnvironmentPath(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for dir, bytes := range map[string]string{first: "binary one", second: "binary two"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "qlty"), []byte(bytes), 0700); err != nil {
			t.Fatal(err)
		}
	}
	firstHash, err := hashQltyExecutableInPath("qlty", first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := hashQltyExecutableInPath("qlty", second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatal("command PATH override did not select the executed Qlty binary")
	}
}

func TestJobCommandResolutionUsesExecutionCwdAndEnvironmentPath(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\nprintf overridden\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "tool"), []byte("#!/bin/sh\nprintf stage\n"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveJobCommand(stage, JobCommand{Command: []string{"tool"}, Environment: map[string]string{"PATH": bin}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0] != filepath.Join(bin, "tool") {
		t.Fatalf("resolved wrong PATH executable: %q", resolved[0])
	}
	local, err := resolveJobCommand(stage, JobCommand{Command: []string{"./tool"}})
	if err != nil {
		t.Fatal(err)
	}
	if local[0] != filepath.Join(stage, "tool") {
		t.Fatalf("relative executable did not resolve under stage: %q", local[0])
	}
}

func TestCopyApprovedQualityTreeVerifiesSourceAndCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "approved", "tools")
	dest := filepath.Join(root, "candidate", "tools")
	t.Cleanup(func() {
		_ = filepath.WalkDir(dest, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	writeApprovedQualityTreeFixture(t, source)
	const tool = "trivy"
	lock := filepath.Join(tool, qualityFixtureScannerPackage+".lock")
	unrelatedLock := filepath.Join(tool, qualityFixtureScannerPackage, "dependency.lock")
	formatterLock := filepath.Join("formatter", "0.10.0-12345678.lock")
	nonemptyLock := filepath.Join("formatter", "0.10.0-87654321.lock")
	requireVerificationSuccess(t, os.Mkdir(filepath.Join(source, "formatter"), 0700))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, lock), nil, 0600))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, unrelatedLock), nil, 0600))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, formatterLock), nil, 0600))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, nonemptyLock), []byte("not an idle lock"), 0600))
	expected, err := hashQualityTree(source)
	requireVerificationSuccess(t, err)
	requireVerificationSuccess(t, copyApprovedQualityTree(context.Background(), source, dest, expected))
	got, err := hashQualityTree(dest)
	requireVerificationSuccess(t, err)
	if got != expected {
		t.Fatal("approved scanner runtime copy changed identity")
	}
	mode, err := os.Stat(filepath.Join(dest, "trivy", qualityFixtureScannerPackage, "scanner"))
	requireVerificationSuccess(t, err)
	if mode.Mode().Perm()&0222 != 0 {
		t.Fatal("copied approved scanner executable remains writable")
	}
	for path, writable := range map[string]bool{lock: true, formatterLock: true, unrelatedLock: false, nonemptyLock: false} {
		info, err := os.Stat(filepath.Join(dest, path))
		requireVerificationSuccess(t, err)
		if got := info.Mode().Perm()&0222 != 0; got != writable {
			t.Fatalf("unexpected write permission for %s: %v", path, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "current")); err != nil {
		t.Fatalf("internal runtime symlink was not preserved: %v", err)
	}
	if err := copyApprovedQualityTree(context.Background(), source, filepath.Join(source, "nested", "tools"), expected); err == nil {
		t.Fatal("runtime source/destination overlap was accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyApprovedQualityTree(canceled, source, filepath.Join(root, "canceled", "tools"), expected); err == nil {
		t.Fatal("canceled runtime copy proceeded")
	}
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, "trivy", qualityFixtureScannerPackage, "scanner"), []byte("replacement"), 0700))
	if err := copyApprovedQualityTree(context.Background(), source, filepath.Join(root, "other", "tools"), expected); err == nil {
		t.Fatal("changed approved source runtime was copied")
	}
	unsafeRoot := filepath.Join(root, "unsafe")
	requireVerificationSuccess(t, os.MkdirAll(unsafeRoot, 0700))
	outside := filepath.Join(root, "outside")
	requireVerificationSuccess(t, os.WriteFile(outside, []byte("outside"), 0600))
	requireVerificationSuccess(t, os.Symlink(outside, filepath.Join(unsafeRoot, "escape")))
	if _, err := hashQualityTree(unsafeRoot); err == nil {
		t.Fatal("runtime tree with external symlink was accepted")
	}
}

func writeApprovedQualityTreeFixture(t *testing.T, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(source, "trivy", qualityFixtureScannerPackage), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "trivy", qualityFixtureScannerPackage, "scanner"), []byte("scanner bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "trivy", qualityFixtureScannerMarker), []byte("done"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../trivy/1.2.3-abcdef012345/scanner", filepath.Join(source, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	pythonBin := filepath.Join(source, "python", "3.11.0-abcdef012345", "bin")
	if err := os.MkdirAll(pythonBin, 0700); err != nil {
		t.Fatal(err)
	}
	python3 := filepath.Join(pythonBin, "python3")
	if err := os.WriteFile(python3, []byte("python runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	trivyBin := filepath.Join(source, "trivy", qualityFixtureScannerPackage, "bin")
	if err := os.MkdirAll(trivyBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(python3, filepath.Join(trivyBin, "python3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("python3", filepath.Join(trivyBin, "python")); err != nil {
		t.Fatal(err)
	}
}
