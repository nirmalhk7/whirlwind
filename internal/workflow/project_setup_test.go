package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type setupExecutor interface {
	runProjectSetup(context.Context, string) error
	dispatchProjectSetup(context.Context)
}

func TestProjectSetupRunsHumanCommandsBeforeReadiness(t *testing.T) {
	for _, failing := range []bool{false, true} {
		name := "success"
		if failing {
			name = "required failure"
		}
		t.Run(name, func(t *testing.T) {
			e, _, repository := testEngine(t)
			runner, ok := any(e).(setupExecutor)
			if !ok {
				t.Fatal("supervisor does not execute durable project setup")
			}
			installSetupRunner(t)
			command := "/usr/bin/true"
			if failing {
				command = "/usr/bin/false"
			}
			configuration := "checks:\n  - name: first\n    command: [" + command + "]\n    timeout_seconds: 10\n  - name: later\n    command: [/usr/bin/true]\n    timeout_seconds: 10\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\n"
			if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project verification commands"}} {
				if out, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("commit policy: %v %s", err, out)
				}
			}
			project, err := e.Project(context.Background(), "p")
			requireWorkflowSuccess(t, err)
			project.Policy, project.PolicyHash, err = policy.Load(repository)
			requireWorkflowSuccess(t, err)
			project.Status = "draft"
			requireWorkflowSuccess(t, e.Store.Put(context.Background(), "projects", "p", project, 1))
			id := store.ID()
			op := model.Operation{Kind: "project_setup", Subject: "p", Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			requireWorkflowSuccess(t, e.Store.Put(context.Background(), "operations", id, op, 0))
			runner.dispatchProjectSetup(context.Background())
			e.background.Wait()
			record, err := e.Store.Get(context.Background(), "operations", id)
			requireWorkflowSuccess(t, err)
			op, err = store.Decode[model.Operation](record)
			requireWorkflowSuccess(t, err)
			var evidence struct {
				Manifest verification.JobManifest `json:"manifest"`
				Report   verification.JobReport   `json:"report"`
			}
			raw, _ := json.Marshal(op.Input)
			requireWorkflowSuccess(t, json.Unmarshal(raw, &evidence))
			wantStage, wantStatus := "complete", "approved"
			if failing {
				wantStage, wantStatus = "failed", "error"
			}
			project, err = e.Project(context.Background(), "p")
			requireWorkflowSuccess(t, err)
			if op.Stage != wantStage || project.Status != wantStatus {
				t.Fatalf("setup outcome not reflected truthfully: operation=%+v project=%+v", op, project)
			}
			if evidence.Manifest.OperationID != id || evidence.Manifest.ProjectID != "p" || evidence.Manifest.PlanID != "" || evidence.Manifest.Purpose != "project_setup" {
				t.Fatalf("setup fabricated plan authority or lost identity: %+v", evidence.Manifest)
			}
			if len(evidence.Report.Results) != 2 || evidence.Report.Results[1].Status != "pass" || evidence.Report.Passed == failing {
				t.Fatalf("human commands not all executed: %+v", evidence.Report)
			}
			requireWorkflowSuccess(t, evidence.Report.Validate(evidence.Manifest))
			requireWorkflowSuccess(t, runner.runProjectSetup(context.Background(), id))
			again, err := e.Store.Get(context.Background(), "operations", id)
			requireWorkflowSuccess(t, err)
			if again.Version != record.Version {
				t.Fatal("terminal setup was rerun")
			}
		})
	}
}

func TestProjectSetupPersistsLiveAndCompletedCheckProgress(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	configuration := "checks:\n  - name: visible-wait\n    command: [" + sleep + ", '3']\n    timeout_seconds: 10\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\n"
	if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project verification commands"}} {
		if out, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("commit policy: %v %s", err, out)
		}
	}
	ctx := context.Background()
	project, err := e.Project(ctx, "p")
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, err = policy.Load(repository)
	requireWorkflowSuccess(t, err)
	project.Status = "draft"
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", "p", project, 1))
	id := store.ID()
	op := model.Operation{Kind: "project_setup", Subject: "p", Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", id, op, 0))
	done := make(chan error, 1)
	go func() { done <- e.runProjectSetup(ctx, id) }()
	deadline := time.Now().Add(2 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		record, getErr := e.Store.Get(ctx, "operations", id)
		if getErr == nil {
			var state struct {
				Stage string `json:"stage"`
				Input struct {
					Progress struct {
						Current string `json:"current"`
					} `json:"progress"`
				} `json:"input"`
			}
			if json.Unmarshal(record.Data, &state) == nil && state.Stage == "running" && state.Input.Progress.Current == "visible-wait" {
				observed = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		<-done
		t.Fatal("active project setup check was not durably visible")
	}
	if err := <-done; err != nil {
		t.Fatalf("project setup: %v", err)
	}
	record, err := e.Store.Get(ctx, "operations", id)
	requireWorkflowSuccess(t, err)
	var state struct {
		Stage string `json:"stage"`
		Input struct {
			Progress struct {
				Current   string `json:"current"`
				Completed []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"completed"`
			} `json:"progress"`
		} `json:"input"`
	}
	requireWorkflowSuccess(t, json.Unmarshal(record.Data, &state))
	if state.Stage != "complete" || state.Input.Progress.Current != "" || len(state.Input.Progress.Completed) != 1 || state.Input.Progress.Completed[0].Name != "visible-wait" || state.Input.Progress.Completed[0].Status != "pass" {
		t.Fatalf("completed setup progress was not retained: %+v", state)
	}
}

func TestAutomaticProjectSetupBuildsFetchAndQualificationPhases(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	policyPath := filepath.Join(repository, ".whirlwind.yaml")
	qualityRoot := filepath.Join(t.TempDir(), "external-qlty")
	configuration := "checks:\n  - name: build\n    command: [/usr/bin/true]\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\nsandbox:\n  allowed_domains: [semgrep.dev]\nquality:\n  root: " + qualityRoot + "\n"
	if err := os.WriteFile(policyPath, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project verification commands"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("commit policy: %v %s", err, output)
		}
	}
	project, err := e.Project(context.Background(), "p")
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, err = policy.Load(repository)
	requireWorkflowSuccess(t, err)
	prepare, err := e.projectSetupManifest(context.Background(), project, "prepare", projectSetupEvidence{Onboarding: true})
	requireWorkflowSuccess(t, err)
	if len(prepare.Commands) != 2 || prepare.Commands[0].Name != "qlty-catalog" || prepare.Commands[1].Command[0] != "curl" || prepare.Commands[1].Command[2] != "https://semgrep.dev/c/p/ci" {
		t.Fatalf("setup does not fetch the real Qlty catalog and pinned Semgrep registry: %+v", prepare.Commands)
	}
	if prepare.Quality == nil || prepare.Quality.Root != qualityRoot {
		t.Fatalf("preparation lacks external Qlty root: %+v", prepare.Quality)
	}
	if err := os.MkdirAll(filepath.Join(qualityRoot, "config", ".qlty"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n", ".semgrep/ci.yaml": "rules:\n  - id: test-rule\n", ".gitleaks.toml": "[extend]\nuseDefault = true\n"} {
		full := filepath.Join(qualityRoot, "config", path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := externalQualitySnapshot(project)
	requireWorkflowSuccess(t, err)
	semgrepConfig := filepath.Join(qualityRoot, "config", ".semgrep", "ci.yaml")
	if err := os.WriteFile(semgrepConfig, []byte("rules:\n  - id: changed-after-prepare\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, staleErr := e.projectSetupManifest(context.Background(), project, "stale-qualify", projectSetupEvidence{Onboarding: true, Phase: "qualify", QualityRoot: snapshot.Root, SnapshotHash: evidenceDigest(snapshot)})
	if staleErr == nil {
		t.Fatal("changed external rules were accepted after preparation")
	}
	if err := os.WriteFile(semgrepConfig, []byte(snapshot.Files[".semgrep/ci.yaml"]), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := []byte("plugins:\n  definitions:\n    semgrep:\n      known_good_version: 1.2.3\n    gitleaks:\n      known_good_version: 4.5.6\n")
	catalogHash, err := verification.HashQltyPluginCatalog(catalog)
	requireWorkflowSuccess(t, err)
	priorRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(priorRoot, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorRoot, "streams", "000.stdout"), catalog, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorRoot, "streams", "000.stderr"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	prior := projectSetupEvidence{Onboarding: true, Phase: "catalog", Manifest: verification.JobManifest{Purpose: verification.ProjectSetupPurpose, ProjectID: project.ID, OperationID: "prepare-catalog", Root: priorRoot}}
	priorOperation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "complete", Input: prior}
	requireWorkflowSuccess(t, e.Store.Put(context.Background(), "operations", "prepare-catalog", priorOperation, 0))
	qualify, err := e.projectSetupManifest(context.Background(), project, "qualify", projectSetupEvidence{Onboarding: true, Phase: "qualify", QualityRoot: snapshot.Root, SnapshotHash: evidenceDigest(snapshot), PreviousOperationID: "prepare-catalog", PluginCatalogHash: catalogHash})
	requireWorkflowSuccess(t, err)
	if qualify.Quality == nil || qualify.Quality.Identity != nil || len(qualify.Commands) != 2 || qualify.Commands[1].ReportFormat != "sarif" || !slices.Equal(qualify.Commands[1].Command, []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}) {
		t.Fatalf("qualification lacks learn-mode frozen configuration and native SARIF scan: %+v", qualify)
	}
}

func TestProjectSetupPreparationDurablyQueuesQualification(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	qualityRoot := filepath.Join(t.TempDir(), "external-qlty")
	configuration := "checks:\n  - name: build\n    command: [/usr/bin/true]\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\nsandbox:\n  allowed_domains: [semgrep.dev]\nquality:\n  root: " + qualityRoot + "\n"
	if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project verification commands"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("commit policy: %v %s", err, output)
		}
	}
	ctx := context.Background()
	project, err := e.Project(ctx, "p")
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, err = policy.Load(repository)
	requireWorkflowSuccess(t, err)
	project.Status = "draft"
	projectRecord, err := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", project.ID, project, projectRecord.Version))
	projectRecord, err = e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	manifest, err := e.projectSetupManifest(ctx, project, "prepare", projectSetupEvidence{Onboarding: true})
	requireWorkflowSuccess(t, err)
	if err := os.MkdirAll(filepath.Join(manifest.Root, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	catalog := "plugins:\n  definitions:\n    semgrep:\n      known_good_version: 1.2.3\n      drivers:\n        lint: {}\n    gitleaks:\n      known_good_version: 4.5.6\n      drivers:\n        lint: {}\n"
	ruleResponse := `{"rule_config":"rules:\n  - id: default-rule\n    message: test\n    languages: [python]\n    severity: WARNING\n    pattern: eval($X)\n"}`
	for index, content := range []string{catalog, ruleResponse} {
		for _, suffix := range []string{"stdout", "stderr"} {
			value := content
			if suffix == "stderr" {
				value = ""
			}
			if err := os.WriteFile(filepath.Join(manifest.Root, "streams", fmt.Sprintf("%03d.%s", index, suffix)), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	id := store.ID()
	op := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "running", Owner: e.owner, Input: projectSetupEvidence{Onboarding: true, Manifest: manifest}}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", id, op, 0))
	record, err := e.Store.Get(ctx, "operations", id)
	requireWorkflowSuccess(t, err)
	op, err = store.Decode[model.Operation](record)
	requireWorkflowSuccess(t, err)
	checkpointCtx, cancelCheckpoint := context.WithCancel(ctx)
	cancelCheckpoint()
	requireWorkflowSuccess(t, e.finishProjectSetupPreparation(checkpointCtx, record, op, projectRecord, project, projectSetupEvidence{Onboarding: true, Manifest: manifest}))
	finished, err := e.Store.Get(ctx, "operations", id)
	requireWorkflowSuccess(t, err)
	finishedOperation, err := store.Decode[model.Operation](finished)
	requireWorkflowSuccess(t, err)
	if finishedOperation.Stage != "complete" {
		t.Fatalf("preparation checkpoint not finished: %+v", finishedOperation)
	}
	operations, err := e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	var next model.Operation
	for _, item := range operations {
		candidate, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if item.ID != id && candidate.Kind == "project_setup" && candidate.Subject == project.ID && candidate.Stage == "queued" {
			next = candidate
		}
	}
	if next.Kind == "" {
		t.Fatal("prepared Qlty defaults did not queue a new qualification operation")
	}
	meta := setupEvidenceFromInput(next.Input)
	canonicalQualityRoot, err := filepath.EvalSymlinks(qualityRoot)
	requireWorkflowSuccess(t, err)
	if !meta.Onboarding || meta.Phase != "catalog" || meta.QualityRoot != canonicalQualityRoot || meta.SnapshotHash == "" {
		t.Fatalf("catalog checkpoint lost setup evidence: %+v", meta)
	}
	savedProject, err := e.Project(ctx, project.ID)
	requireWorkflowSuccess(t, err)
	if savedProject.Status != "draft" {
		t.Fatalf("project became ready before scanner qualification: %s", savedProject.Status)
	}
	for _, path := range []string{".qlty/qlty.toml", ".semgrep/ci.yaml", ".gitleaks.toml"} {
		if _, err := os.Stat(filepath.Join(qualityRoot, "config", path)); err != nil {
			t.Fatalf("default file not published outside source: %s: %v", path, err)
		}
	}
	var catalogID string
	for _, item := range operations {
		candidate, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if candidate.Kind == "project_setup" && candidate.Subject == project.ID && candidate.Stage == "queued" && setupEvidenceFromInput(candidate.Input).Phase == "catalog" {
			catalogID = item.ID
		}
	}
	if catalogID == "" {
		t.Fatal("effective catalog probe operation missing")
	}
	catalogOperation, err := e.Store.Get(ctx, "operations", catalogID)
	requireWorkflowSuccess(t, err)
	catalogOperationValue, err := store.Decode[model.Operation](catalogOperation)
	requireWorkflowSuccess(t, err)
	catalogInput := setupEvidenceFromInput(catalogOperationValue.Input)
	catalogManifest, err := e.projectSetupManifest(ctx, project, catalogID, catalogInput)
	requireWorkflowSuccess(t, err)
	if len(catalogManifest.Commands) != 1 || catalogManifest.Commands[0].Name != "qlty-effective-catalog" {
		t.Fatalf("effective catalog is not probed after config publication: %+v", catalogManifest.Commands)
	}
	if err := os.MkdirAll(filepath.Join(catalogManifest.Root, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"stdout", "stderr"} {
		value := catalog
		if suffix == "stderr" {
			value = ""
		}
		if err := os.WriteFile(filepath.Join(catalogManifest.Root, "streams", "000."+suffix), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalogEvidence := projectSetupEvidence{Onboarding: true, Phase: "catalog", QualityRoot: catalogInput.QualityRoot, SnapshotHash: catalogInput.SnapshotHash, Manifest: catalogManifest}
	catalogInput.Manifest = catalogManifest
	catalogInput.Onboarding, catalogInput.Phase = true, "catalog"
	catalogOperationValue.Stage, catalogOperationValue.Owner, catalogOperationValue.Input = "running", e.owner, catalogInput
	requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", catalogID, catalogOperationValue, catalogOperation.Version))
	catalogOperationRecord, err := e.Store.Get(ctx, "operations", catalogID)
	requireWorkflowSuccess(t, err)
	catalogOperationValue, err = store.Decode[model.Operation](catalogOperationRecord)
	requireWorkflowSuccess(t, err)
	projectRecord, err = e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	savedProject, err = store.Decode[model.Project](projectRecord)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.finishProjectSetupCatalogProbe(ctx, catalogOperationRecord, catalogOperationValue, projectRecord, savedProject, catalogEvidence))
	operations, err = e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	for _, item := range operations {
		candidate, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if item.ID != catalogID && candidate.Kind == "project_setup" && candidate.Subject == project.ID && candidate.Stage == "queued" {
			meta := setupEvidenceFromInput(candidate.Input)
			if meta.Phase != "qualify" || meta.PreviousOperationID != catalogID || meta.PluginCatalogHash == "" {
				t.Fatalf("qualification checkpoint lacks effective catalog: %+v", meta)
			}
			break
		}
	}
}

func TestProjectSetupAcceptsOnlyCompletedQltyBaselineFindings(t *testing.T) {
	root := t.TempDir()
	manifest := verification.JobManifest{
		Version: 1, Purpose: verification.ProjectSetupPurpose, ProjectID: "project", OperationID: "setup", JobID: "setup",
		ConfigurationHash: "configuration", CandidateCommit: strings.Repeat("a", 40), CandidateTree: strings.Repeat("b", 40), BaseCommit: strings.Repeat("a", 40),
		Repository: filepath.Join(root, "source"), Root: filepath.Join(root, "quality", "jobs", "setup"), Launcher: filepath.Join(root, "nono"),
		Quality:  &model.QualitySnapshot{Root: filepath.Join(root, "quality"), Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n"}, PluginCatalog: []byte("plugins: {}\n")},
		Commands: []verification.JobCommand{{Name: "build", Command: []string{"/usr/bin/true"}, TimeoutSeconds: 10}, {Name: "quality", Command: []string{"qlty", "check", "--sarif"}, TimeoutSeconds: 10, ReportFormat: "sarif"}},
	}
	identity := &model.QualityIdentity{Version: 1, QltyVersion: "0.668.0 macos-arm64 (fixture)", QltyBinarySHA256: strings.Repeat("a", 64), ConfigurationHash: strings.Repeat("b", 64), ToolRuntimeHash: strings.Repeat("c", 64), PluginCatalogHash: strings.Repeat("d", 64), ToolRuntimePath: filepath.Join(root, "runtime"), Tools: []model.QualityToolIdentity{{Name: "gitleaks", Version: "1.0", Fingerprint: strings.Repeat("e", 8)}, {Name: "semgrep", Version: "1.0", Fingerprint: strings.Repeat("f", 8)}}}
	zero, one := 0, 1
	manifestHash := manifest.Digest()
	report := verification.JobReport{Purpose: manifest.Purpose, ProjectID: manifest.ProjectID, OperationID: manifest.OperationID, ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree, Passed: false, QualityIdentity: identity, Results: []verification.JobResult{
		{Result: verification.Result{Name: "build", Command: manifest.Commands[0].Command, ExitCode: &zero, Passed: true, Runtime: verification.RuntimeIdentity{CleanupVerified: true}}, Status: "pass", ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree},
		{Result: verification.Result{Name: "quality", Command: manifest.Commands[1].Command, ExitCode: &one, Passed: false, QualityIdentity: identity, Runtime: verification.RuntimeIdentity{CleanupVerified: true}, Error: "exit status 1"}, Status: "fail", ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree},
	}}
	if err := os.MkdirAll(filepath.Join(manifest.Root, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"results":[{"ruleId":"semgrep:fixture","level":"warning","message":{"text":"existing finding"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"app.py"},"region":{"startLine":1,"snippet":{"text":"eval(value)"}}}}]}],"invocations":[{"executionSuccessful":true,"toolExecutionNotifications":[],"toolConfigurationNotifications":[]}]}]}`
	stream := filepath.Join(manifest.Root, "streams", "001.stdout")
	if err := os.WriteFile(stream, []byte(sarif), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Root, "streams", "001.stderr"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(manifest); err != nil {
		t.Fatalf("fixture report invalid: %v", err)
	}
	if err := verification.ValidateJobSARIF([]byte(sarif)); err != nil {
		t.Fatalf("fixture SARIF invalid: %v", err)
	}
	if !projectSetupAllowsExistingQltyFindings(manifest, report) {
		t.Fatal("complete existing Qlty findings with valid runtime identity did not qualify setup")
	}
	if err := os.WriteFile(stream, []byte("malformed SARIF"), 0600); err != nil {
		t.Fatal(err)
	}
	if projectSetupAllowsExistingQltyFindings(manifest, report) {
		t.Fatal("malformed Qlty SARIF qualified setup")
	}
	if err := os.WriteFile(stream, []byte(sarif), 0600); err != nil {
		t.Fatal(err)
	}
	failedBuild := report
	failedBuild.Results = append([]verification.JobResult(nil), report.Results...)
	failedBuild.Results[0].Status, failedBuild.Results[0].Passed = "fail", false
	if projectSetupAllowsExistingQltyFindings(manifest, failedBuild) {
		t.Fatal("Qlty findings waived a failed required build check")
	}
	failedInvocation := report
	failedInvocation.Results = append([]verification.JobResult(nil), report.Results...)
	failedInvocation.Results[1].Status = "error"
	if projectSetupAllowsExistingQltyFindings(manifest, failedInvocation) {
		t.Fatal("failed Qlty invocation qualified setup")
	}
	otherFailure := report
	otherFailure.Results = append([]verification.JobResult(nil), report.Results...)
	otherFailure.Results[1].Error = "network unavailable"
	if projectSetupAllowsExistingQltyFindings(manifest, otherFailure) {
		t.Fatal("non-finding Qlty failure qualified setup")
	}
	unclean := report
	unclean.Results = append([]verification.JobResult(nil), report.Results...)
	unclean.Results[1].Runtime.CleanupVerified = false
	if projectSetupAllowsExistingQltyFindings(manifest, unclean) {
		t.Fatal("unverified Qlty runtime cleanup qualified setup")
	}
}

func installSetupRunner(t *testing.T) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "whirlwind-verify")
	command := exec.Command("go", "build", "-o", binary, "./cmd/whirlwind-verify")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build verifier: %v %s", err, output)
	}
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(repository, "bridges", "nono", "launch.mjs"))
}
