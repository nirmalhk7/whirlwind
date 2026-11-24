package verification

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func stagingJobFixture(t *testing.T, commands ...JobCommand) JobManifest {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-C", repo}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("prepare source fixture: git %v: %v: %s", args, err, output)
		}
		return string(bytes.TrimSpace(output))
	}
	git("init", "-b", "fixture")
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(repo, "source.txt"), []byte("frozen source\n"), 0600))
	git("add", "source.txt")
	git("commit", "-m", "fixture")
	head := git("rev-parse", "HEAD")
	return JobManifest{Version: 1, JobID: "staging-test", PlanID: "plan", PlanVersion: 1, PlanHash: "frozen plan",
		ConfigurationHash: "frozen-config", CandidateCommit: head, CandidateTree: git("rev-parse", "HEAD^{tree}"),
		BaseCommit: head, Root: filepath.Join(t.TempDir(), "job"), Repository: repo, Launcher: "/fixture/launcher",
		Commands: commands}
}

func stagingJobOpen(t *testing.T, manifest JobManifest) *jobRunner {
	t.Helper()
	r, err := openJob(manifest)
	requireVerificationSuccess(t, err)
	return r
}

func runStagingJob(t *testing.T, manifest JobManifest, observe func(JobEvent) error) (JobReport, error) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		return RunJob(t.Context(), manifest, observe)
	}
	// The legacy public entry point only admits native execution on macOS.
	r := stagingJobOpen(t, manifest)
	defer r.close()
	return r.run(t.Context(), observe)
}

func TestVerificationRetriesAfterStagingMarkerWriteFails(t *testing.T) {
	m := stagingJobFixture(t, JobCommand{Name: "verify", Command: []string{"/must/not/run"}, TimeoutSeconds: 5})
	markerTemp := filepath.Join(m.Root, checkoutOwnershipFile(verificationCheckoutDir)+".tmp")
	requireVerificationSuccess(t, os.MkdirAll(markerTemp, 0700))
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(markerTemp, "blocker"), []byte("preserve"), 0600))
	observe := func(JobEvent) error {
		t.Fatal("command started before checkout ownership was published")
		return nil
	}
	if _, err := runStagingJob(t, m, observe); err == nil || !strings.Contains(err.Error(), "record verification staging ownership") {
		t.Fatalf("expected staging marker failure, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, verificationCheckoutDir)); !os.IsNotExist(err) {
		t.Fatalf("published a checkout without recorded ownership: %v", err)
	}
	requireVerificationSuccess(t, os.RemoveAll(markerTemp))
	interrupted := errors.New("stop before executing the frozen command")
	_, err := runStagingJob(t, m, func(event JobEvent) error {
		if event.Type != "started" || event.Index != 0 {
			t.Fatalf("unexpected event after retry: %+v", event)
		}
		content, err := os.ReadFile(filepath.Join(m.Root, verificationCheckoutDir, "source.txt"))
		requireVerificationSuccess(t, err)
		if string(content) != "frozen source\n" {
			t.Fatalf("checkout did not restore frozen source: %q", content)
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatalf("did not recover and reach the first command after the marker failure: %v", err)
	}
}

func TestVerificationRecoversOnlyOwnedUnpublishedCheckouts(t *testing.T) {
	for _, scenario := range []string{"frozen-checkout", "autofix-checkout", "replaced-checkout"} {
		t.Run(scenario, func(t *testing.T) {
			m := stagingJobFixture(t, JobCommand{Name: "verify", Command: []string{"/must/not/run"}, TimeoutSeconds: 5})
			relative := verificationCheckoutDir
			if scenario == "autofix-checkout" {
				m.Commands[0].Mode = "autofix"
				relative = autofixCheckoutDir(0)
			}
			interrupted := errors.New("stop after git clone --no-checkout")
			var draft string
			func() {
				r := stagingJobOpen(t, m)
				defer r.close()
				_, err := r.cloneDisposableCheckout(t.Context(), relative, func(stage string) error {
					draft = stage
					if _, err := os.Stat(filepath.Join(stage, ".git", "HEAD")); err != nil {
						t.Fatalf("fixture did not clone with real Git: %v", err)
					}
					if _, err := os.Lstat(filepath.Join(stage, "source.txt")); !os.IsNotExist(err) {
						t.Fatalf("fixture already populated the checkout: %v", err)
					}
					return interrupted
				})
				if !errors.Is(err, interrupted) {
					t.Fatalf("expected interrupted Git preparation, got %v", err)
				}
			}()
			if scenario == "replaced-checkout" {
				requireVerificationSuccess(t, os.Rename(draft, draft+".original"))
				requireVerificationSuccess(t, os.Mkdir(draft, 0700))
				requireVerificationSuccess(t, os.WriteFile(filepath.Join(draft, "untouched"), []byte("replacement"), 0600))
			}
			r := stagingJobOpen(t, m)
			defer r.close()
			var stage string
			var err error
			if scenario == "autofix-checkout" {
				stage, err = r.prepareFixCheckout(t.Context(), 0)
			} else {
				stage, err = r.prepareCheckout(t.Context())
			}
			if scenario == "replaced-checkout" {
				if err == nil || !strings.Contains(err.Error(), "unrecognized verification staging owner") {
					t.Fatalf("retry accepted a replacement directory: %v", err)
				}
				content, err := os.ReadFile(filepath.Join(draft, "untouched"))
				requireVerificationSuccess(t, err)
				if string(content) != "replacement" {
					t.Fatalf("changed replacement directory: %q", content)
				}
				return
			}
			requireVerificationSuccess(t, err)
			content, err := os.ReadFile(filepath.Join(stage, "source.txt"))
			requireVerificationSuccess(t, err)
			if string(content) != "frozen source\n" {
				t.Fatalf("retry did not restore frozen source: %q", content)
			}
			if _, err := os.Lstat(draft); !os.IsNotExist(err) {
				t.Fatalf("retry kept the abandoned clone: %v", err)
			}
		})
	}
}

func stagingResult(t *testing.T, runner *jobRunner, index int, cleanupVerified bool) JobResult {
	t.Helper()
	m := runner.manifest
	command := m.Commands[index]
	requireVerificationSuccess(t, runner.root.MkdirAll("streams", 0700))
	// These tests exercise actual Git staging and journal recovery without
	// invoking commands through the macOS-only execution runtime.
	exit, status, passed := 0, "pass", true
	if !cleanupVerified {
		exit, status, passed = 1, "error", false
	}
	return JobResult{
		Result: Result{Name: command.Name, Command: command.Command, TimeoutSeconds: command.TimeoutSeconds, Passed: passed, ExitCode: &exit,
			Runtime: RuntimeIdentity{ExecutionID: command.Name, CleanupVerified: cleanupVerified}, Output: "retained run output"},
		Status: status, ManifestHash: m.Digest(), CandidateCommit: m.CandidateCommit, CandidateTree: m.CandidateTree, ConfigurationHash: m.ConfigurationHash,
	}
}

func journalStagingResult(t *testing.T, runner *jobRunner, index int, result JobResult) {
	t.Helper()
	hash := runner.manifest.Digest()
	requireVerificationSuccess(t, runner.record(JobEvent{Type: "started", Index: index, ManifestHash: hash}, nil))
	requireVerificationSuccess(t, runner.record(JobEvent{Type: "result", Index: index, ManifestHash: hash, Result: &result}, nil))
	runner.results[index], runner.started[index] = result, true
}

func TestCompletedVerificationReleasesStagingAndRetainsEvidence(t *testing.T) {
	ctx := t.Context()
	falseValue := false
	verify := JobCommand{Name: "verify", Command: []string{"/irrelevant/verify"}, OutputPaths: []string{"reports/result.txt"}, TimeoutSeconds: 5}
	fix := JobCommand{Name: "fix", Mode: "autofix", Command: []string{"/irrelevant/autofix"}, TimeoutSeconds: 5, Required: &falseValue}
	qlty := JobCommand{Name: "scanner", Command: []string{"qlty", "check", "--sarif"}, TimeoutSeconds: 5}
	m := stagingJobFixture(t, verify, fix, qlty)
	// A setup job can record its newly qualified scanner without a prior
	// approved tool tree. Frozen configuration and tools live outside checkout.
	m.Purpose, m.ProjectID, m.OperationID = ProjectSetupPurpose, "project", m.JobID
	m.PlanID, m.PlanVersion, m.PlanHash = "", 0, ""
	qualityRoot, err := filepath.EvalSymlinks(filepath.Dir(m.Root))
	requireVerificationSuccess(t, err)
	m.Root = filepath.Join(qualityRoot, "jobs", m.JobID)
	m.Quality = &model.QualitySnapshot{Root: qualityRoot, Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n", ".gitleaks.toml": "[extend]\nuseDefault = true\n"},
		PluginCatalog: []byte("plugins:\n  gitleaks:\n    version: 1.2.3\n")}
	requireVerificationSuccess(t, m.validateDefinition())
	var artifact JobResult
	var fixResult JobResult
	var identity *model.QualityIdentity
	func() {
		r := stagingJobOpen(t, m)
		defer r.close()
		stage, err := r.prepareCheckout(ctx)
		requireVerificationSuccess(t, err)
		requireVerificationSuccess(t, os.Mkdir(filepath.Join(stage, "reports"), 0700))
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(stage, "reports", "result.txt"), []byte("retained evidence\n"), 0600))
		artifact = stagingResult(t, r, 0, true)
		artifact.Artifacts, err = r.captureArtifacts(stage, 0, verify.OutputPaths)
		requireVerificationSuccess(t, err)
		journalStagingResult(t, r, 0, artifact)
		fixStage, err := r.prepareFixCheckout(ctx, 1)
		requireVerificationSuccess(t, err)
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(fixStage, "proposed.txt"), []byte("corrected content\n"), 0600))
		fixResult = stagingResult(t, r, 1, true)
		fixResult.Autofix, err = r.captureAutofix(ctx, fixStage, 1, fix)
		requireVerificationSuccess(t, err)
		if fixResult.Autofix == nil {
			t.Fatal("fixture did not capture patch")
		}
		journalStagingResult(t, r, 1, fixResult)
		runtime := filepath.Join(m.Root, "runtime", "scanner")
		tools := filepath.Join(runtime, "home", ".qlty", "cache", "tools", "gitleaks")
		packageDir := filepath.Join(tools, "1.2.3-abcdef012345")
		requireVerificationSuccess(t, os.MkdirAll(packageDir, 0700))
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(packageDir, "gitleaks"), []byte("retained scanner runtime fixture"), 0700))
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(tools, "1.2.3-abcdef012345.done"), nil, 0600))
		executable := filepath.Join(runtime, "qlty-fixture")
		requireVerificationSuccess(t, os.WriteFile(executable, []byte("isolated qlty identity fixture"), 0700))
		reportOutput := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","semanticVersion":"0.668.0","version":"0.668.0 fixture"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`
		identity, err = captureQualityIdentity(reportOutput, runtime, executable, m.Quality, "")
		requireVerificationSuccess(t, err)
		result := stagingResult(t, r, 2, true)
		result.QualityIdentity = identity
		journalStagingResult(t, r, 2, result)
		for _, name := range []string{"streams/000.stdout", "streams/000.stderr"} {
			requireVerificationSuccess(t, os.WriteFile(filepath.Join(m.Root, name), []byte("stream evidence\n"), 0600))
		}
	}()
	run := func() {
		t.Helper()
		report, err := runStagingJob(t, m, func(JobEvent) error { t.Error("completed commands must not replay"); return nil })
		requireVerificationSuccess(t, err)
		if !report.Passed || !model.SameQualityIdentity(report.QualityIdentity, identity) {
			t.Fatalf("evidence changed: %+v", report)
		}
	}
	run()
	for i := 0; i < 2; i++ {
		for _, path := range []string{verificationCheckoutDir, autofixCheckoutDir(1)} {
			for _, name := range []string{path, checkoutOwnershipFile(path)} {
				if _, err := os.Lstat(filepath.Join(m.Root, name)); !os.IsNotExist(err) {
					t.Errorf("completed disposable staging still exists: %s: %v", name, err)
				}
			}
		}
		saved, err := readJobReport(m)
		requireVerificationSuccess(t, err)
		patch, err := ReadAutofixPatch(m, 1, saved.Results[1])
		requireVerificationSuccess(t, err)
		if !bytes.Contains(patch, []byte("+corrected content")) {
			t.Fatalf("lost patch: %q", patch)
		}
		retained, err := os.ReadFile(filepath.Join(m.Root, artifact.Artifacts[0].Location))
		requireVerificationSuccess(t, err)
		if string(retained) != "retained evidence\n" {
			t.Fatalf("lost retained artifact: %q", retained)
		}
		hash, err := hashQualityTree(identity.ToolRuntimePath)
		requireVerificationSuccess(t, err)
		if hash != identity.ToolRuntimeHash {
			t.Fatal("changed retained scanner runtime")
		}
		out, stderr, err := ReadJobStreams(m.Root, 0)
		requireVerificationSuccess(t, err)
		if out != "stream evidence\n" || stderr != out {
			t.Fatal("lost retained streams")
		}
		if _, err := os.Stat(filepath.Join(m.Root, jobJournalFilename)); err != nil {
			t.Fatal(err)
		}
		run()
	}
}

func TestInterruptedVerificationRetainsStagingAndNeverReplays(t *testing.T) {
	m := stagingJobFixture(t, JobCommand{Name: "uncertain", Command: []string{"/must/not/run"}, TimeoutSeconds: 5})
	func() {
		r := stagingJobOpen(t, m)
		defer r.close()
		stage, err := r.prepareCheckout(t.Context())
		requireVerificationSuccess(t, err)
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(stage, "partial-output"), []byte("partial evidence"), 0600))
		requireVerificationSuccess(t, r.record(JobEvent{Type: "started", Index: 0, ManifestHash: m.Digest()}, nil))
	}()
	for i := 0; i < 2; i++ {
		r := stagingJobOpen(t, m)
		report, err := r.run(t.Context(), nil)
		r.close()
		requireVerificationSuccess(t, err)
		if report.Passed || report.Results[0].Error != "interrupted command: effects uncertain; command was not replayed" {
			t.Fatalf("uncertain command not recorded correctly: %+v", report.Results)
		}
		body, err := os.ReadFile(filepath.Join(m.Root, "checkout", "partial-output"))
		if err != nil || string(body) != "partial evidence" {
			t.Fatalf("lost uncertain staging evidence: %q %v", body, err)
		}
	}
}

func TestVerificationDoesNotReleaseStagingIfReportWasNotSaved(t *testing.T) {
	m := stagingJobFixture(t, JobCommand{Name: "verify", Command: []string{"/irrelevant/verify"}, TimeoutSeconds: 5})
	r := stagingJobOpen(t, m)
	defer r.close()
	stage, err := r.prepareCheckout(t.Context())
	requireVerificationSuccess(t, err)
	journalStagingResult(t, r, 0, stagingResult(t, r, 0, true))
	requireVerificationSuccess(t, os.Mkdir(filepath.Join(m.Root, "report.json"), 0700))
	if _, err := r.run(t.Context(), nil); err == nil {
		t.Fatal("accepted an unpersistable report")
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("did not preserve staging before the report was saved: %v", err)
	}
}

func TestCompletedVerificationResumesPartialStagingRemoval(t *testing.T) {
	m := stagingJobFixture(t, JobCommand{Name: "done", Command: []string{"/irrelevant/verify"}, TimeoutSeconds: 5})
	stage := filepath.Join(m.Root, verificationCheckoutDir)
	func() {
		r := stagingJobOpen(t, m)
		defer r.close()
		_, err := r.prepareCheckout(t.Context())
		requireVerificationSuccess(t, err)
		journalStagingResult(t, r, 0, stagingResult(t, r, 0, true))
		// Simulate partial cleanup before reopening the journal.
		requireVerificationSuccess(t, os.RemoveAll(filepath.Join(stage, ".git")))
	}()
	r := stagingJobOpen(t, m)
	defer r.close()
	_, err := r.run(t.Context(), nil)
	requireVerificationSuccess(t, err)
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("partial removal did not finish: %v", err)
	}
}

func TestCompletedVerificationKeepsUnownedOrUnverifiedStaging(t *testing.T) {
	for _, scenario := range []string{"legacy-unmarked", "replaced-owned-directory", "runtime-cleanup-unverified"} {
		t.Run(scenario, func(t *testing.T) {
			m := stagingJobFixture(t, JobCommand{Name: "done", Command: []string{"/irrelevant/verify"}, TimeoutSeconds: 5})
			r := stagingJobOpen(t, m)
			defer r.close()
			stage, err := r.prepareCheckout(t.Context())
			requireVerificationSuccess(t, err)
			switch scenario {
			case "legacy-unmarked":
				requireVerificationSuccess(t, os.Remove(filepath.Join(m.Root, checkoutOwnershipFile(verificationCheckoutDir))))
			case "replaced-owned-directory":
				requireVerificationSuccess(t, os.Rename(stage, stage+".other"))
				requireVerificationSuccess(t, os.Mkdir(stage, 0700))
				if _, err := r.prepareCheckout(t.Context()); err == nil || !strings.Contains(err.Error(), "unrecognized verification staging owner") {
					t.Fatalf("preparation accepted replacement staging: %v", err)
				}
			}
			requireVerificationSuccess(t, os.WriteFile(filepath.Join(stage, "untouched"), []byte("preserve"), 0600))
			result := stagingResult(t, r, 0, scenario != "runtime-cleanup-unverified")
			journalStagingResult(t, r, 0, result)
			_, err = r.run(t.Context(), nil)
			requireVerificationSuccess(t, err)
			content, err := os.ReadFile(filepath.Join(stage, "untouched"))
			if err != nil || string(content) != "preserve" {
				t.Fatalf("deleted unowned or unreconciled data: %q %v", content, err)
			}
		})
	}
}
