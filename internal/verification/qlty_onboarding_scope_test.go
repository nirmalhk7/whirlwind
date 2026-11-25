package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"whirlwind/internal/model"
)

const (
	baselineProbe    = "baseline-probe.txt"
	candidateProbe   = "new-candidate-probe.txt"
	scopeGitRevParse = "rev-parse"
)

func TestQltyStagingSeparatesUnchangedOnboardingFromCandidateDiff(t *testing.T) {
	ctx := context.Background()
	fixture := t.TempDir()
	repository := filepath.Join(fixture, "repository")
	requireVerificationSuccess(t, os.Mkdir(repository, 0700))
	nativeDefaultGit(t, repository, "init", "--template=")
	const unchanged = "unchanged file already committed at onboarding\n"
	const edited = "changed candidate bytes\n"
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(repository, baselineProbe), []byte(unchanged), 0600))
	nativeDefaultGit(t, repository, "add", "--", baselineProbe)
	nativeDefaultGit(t, repository, "-c", "commit.gpgsign=false", "commit", "-m", "fixture baseline")
	base := nativeDefaultGit(t, repository, scopeGitRevParse, "HEAD")

	jobRoot := filepath.Join(fixture, "job")
	requireVerificationSuccess(t, os.Mkdir(jobRoot, 0700))
	root, err := os.OpenRoot(jobRoot)
	requireVerificationSuccess(t, err)
	t.Cleanup(func() { _ = root.Close() })
	runner := jobRunner{root: root, manifest: JobManifest{
		Purpose: ProjectSetupPurpose, Repository: repository, BaseCommit: base, CandidateCommit: base,
		Quality: &model.QualitySnapshot{Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n"}},
	}}
	staged, err := runner.prepareFixCheckout(ctx, 0)
	requireVerificationSuccess(t, err)
	assertQltyStagingDelta(t, ctx, &runner, staged, base, "", "")
	actual, err := os.ReadFile(filepath.Join(staged, baselineProbe))
	requireVerificationSuccess(t, err)
	if string(actual) != unchanged {
		t.Fatalf("onboarding lost committed baseline content: %q", actual)
	}

	requireVerificationSuccess(t, os.WriteFile(filepath.Join(repository, baselineProbe), []byte(edited), 0600))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(repository, candidateProbe), []byte("new candidate bytes\n"), 0600))
	nativeDefaultGit(t, repository, "add", "--", baselineProbe, candidateProbe)
	nativeDefaultGit(t, repository, "-c", "commit.gpgsign=false", "commit", "-m", "fixture candidate")
	candidate := nativeDefaultGit(t, repository, scopeGitRevParse, "HEAD")
	runner.manifest.Purpose, runner.manifest.CandidateCommit = "", candidate
	staged, err = runner.prepareFixCheckout(ctx, 1)
	requireVerificationSuccess(t, err)
	assertQltyStagingDelta(t, ctx, &runner, staged, base, baselineProbe, candidateProbe)
	actual, err = os.ReadFile(filepath.Join(staged, baselineProbe))
	requireVerificationSuccess(t, err)
	if string(actual) != edited {
		t.Fatalf("staging restored base bytes instead of preserving candidate: %q", actual)
	}
	if head := nativeDefaultGit(t, repository, scopeGitRevParse, "HEAD"); head != candidate {
		t.Fatalf("scope staging changed original repository HEAD: %s != %s", head, candidate)
	}
	if changes := nativeDefaultGit(t, repository, "status", "--porcelain"); changes != "" {
		t.Fatalf("scope staging modified original repository: %s", changes)
	}
}

func assertQltyStagingDelta(t *testing.T, ctx context.Context, runner *jobRunner, stage, base, tracked, untracked string) {
	t.Helper()
	head, err := runner.git(ctx, stage, scopeGitRevParse, "HEAD")
	requireVerificationSuccess(t, err)
	changed, err := runner.git(ctx, stage, "diff", "--name-only", "HEAD", "--", baselineProbe, candidateProbe)
	requireVerificationSuccess(t, err)
	newFiles, err := runner.git(ctx, stage, "ls-files", "--others", "--exclude-standard", "--", baselineProbe, candidateProbe)
	requireVerificationSuccess(t, err)
	if head != base || changed != tracked || newFiles != untracked {
		t.Fatalf("wrong HEAD-diff stage: HEAD=%q changed=%q untracked=%q", head, changed, newFiles)
	}
}
