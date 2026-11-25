package workflow

import (
	"context"
	"encoding/json"
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

const (
	setupFixturePolicyFile     = ".whirlwind.yaml"
	setupFixtureCommitSubject  = "Confirm project verification commands"
	setupFixtureDisableHooks   = "core.hooksPath=/dev/null"
	setupFixtureCommitError    = "commit policy: %v %s"
	setupFixtureGitleaksConfig = ".gitleaks.toml"
	setupFixtureQltyConfig     = ".qlty/qlty.toml"
	setupFixtureCatalog        = "prepare-catalog"
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
			commitSetupFixturePolicy(t, repository, configuration)
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
			version := assertHumanSetupOutcome(t, e, id, failing)
			requireWorkflowSuccess(t, runner.runProjectSetup(context.Background(), id))
			again, err := e.Store.Get(context.Background(), "operations", id)
			requireWorkflowSuccess(t, err)
			if again.Version != version {
				t.Fatal("terminal setup was rerun")
			}
		})
	}
}

func assertHumanSetupOutcome(t *testing.T, e *Engine, id string, failing bool) int {
	t.Helper()
	record, err := e.Store.Get(context.Background(), "operations", id)
	requireWorkflowSuccess(t, err)
	op, err := store.Decode[model.Operation](record)
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
	project, err := e.Project(context.Background(), "p")
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
	return record.Version
}

func TestProjectSetupPersistsLiveAndCompletedCheckProgress(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	configuration := "checks:\n  - name: visible-wait\n    command: [" + sleep + ", '3']\n    timeout_seconds: 10\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\n"
	commitSetupFixturePolicy(t, repository, configuration)
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
	if !waitForLiveSetupProgress(ctx, e, id) {
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

func waitForLiveSetupProgress(ctx context.Context, e *Engine, id string) bool {
	deadline := time.Now().Add(2 * time.Second)
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
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestAutomaticProjectSetupBuildsCatalogAndQualificationWithoutNetworkGrant(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	qualityRoot := filepath.Join(t.TempDir(), "external-qlty")
	configuration := "checks:\n  - name: build\n    command: [/usr/bin/true]\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\nquality:\n  root: " + qualityRoot + "\n"
	commitSetupFixturePolicy(t, repository, configuration)
	project, err := e.Project(context.Background(), "p")
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, err = policy.Load(repository)
	requireWorkflowSuccess(t, err)
	prepare, err := e.projectSetupManifest(context.Background(), project, "prepare", projectSetupEvidence{Onboarding: true})
	requireWorkflowSuccess(t, err)
	if len(prepare.Commands) != 1 || !slices.Equal(prepare.Commands[0].Command, []string{"qlty", "config", "show", "--no-upgrade-check"}) {
		t.Fatalf("setup preparation should only inspect the Qlty catalog: %+v", prepare.Commands)
	}
	if prepare.Quality == nil || prepare.Quality.Root != qualityRoot {
		t.Fatalf("preparation lacks external Qlty root: %+v", prepare.Quality)
	}
	writePreparedSetupFixtureConfig(t, qualityRoot)
	snapshot := assertChangedSetupConfigIsRefused(t, e, project, qualityRoot)
	catalogHash := retainSetupFixtureCatalog(t, e, project)
	qualify, err := e.projectSetupManifest(context.Background(), project, "qualify", projectSetupEvidence{Onboarding: true, Phase: "qualify", QualityRoot: snapshot.Root, SnapshotHash: evidenceDigest(snapshot), PreviousOperationID: setupFixtureCatalog, PluginCatalogHash: catalogHash})
	requireWorkflowSuccess(t, err)
	if qualify.Quality == nil || qualify.Quality.Identity != nil || len(qualify.Commands) != 2 || qualify.Commands[1].ReportFormat != "sarif" || !slices.Equal(qualify.Commands[1].Command, []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}) {
		t.Fatalf("qualification lacks learn-mode frozen configuration and native SARIF scan: %+v", qualify)
	}
}

func writePreparedSetupFixtureConfig(t *testing.T, qualityRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(qualityRoot, "config", ".qlty"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{setupFixtureQltyConfig: "config_version = \"0\"\n", setupFixtureGitleaksConfig: "[extend]\nuseDefault = true\n"} {
		full := filepath.Join(qualityRoot, "config", path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertChangedSetupConfigIsRefused(t *testing.T, e *Engine, project model.Project, qualityRoot string) model.QualitySnapshot {
	t.Helper()
	snapshot, err := externalQualitySnapshot(project)
	requireWorkflowSuccess(t, err)
	secretConfig := filepath.Join(qualityRoot, "config", setupFixtureGitleaksConfig)
	if err := os.WriteFile(secretConfig, []byte("# changed after preparation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, staleErr := e.projectSetupManifest(context.Background(), project, "stale-qualify", projectSetupEvidence{Onboarding: true, Phase: "qualify", QualityRoot: snapshot.Root, SnapshotHash: evidenceDigest(snapshot)})
	if staleErr == nil {
		t.Fatal("changed external rules were accepted after preparation")
	}
	if err := os.WriteFile(secretConfig, []byte(snapshot.Files[setupFixtureGitleaksConfig]), 0600); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func retainSetupFixtureCatalog(t *testing.T, e *Engine, project model.Project) string {
	t.Helper()
	catalog := []byte("plugins:\n  definitions:\n    gitleaks:\n      known_good_version: 4.5.6\n")
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
	prior := projectSetupEvidence{Onboarding: true, Phase: "catalog", Manifest: verification.JobManifest{Purpose: verification.ProjectSetupPurpose, ProjectID: project.ID, OperationID: setupFixtureCatalog, Root: priorRoot}}
	priorOperation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "complete", Input: prior}
	requireWorkflowSuccess(t, e.Store.Put(context.Background(), "operations", setupFixtureCatalog, priorOperation, 0))
	return catalogHash
}

func TestProjectSetupPreparationDurablyQueuesQualification(t *testing.T) {
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	qualityRoot := filepath.Join(t.TempDir(), "external-qlty")
	configuration := "checks:\n  - name: build\n    command: [/usr/bin/true]\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\nquality:\n  root: " + qualityRoot + "\n"
	commitSetupFixturePolicy(t, repository, configuration)
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
	catalog := "plugins:\n  definitions:\n    gitleaks:\n      known_good_version: 4.5.6\n      drivers:\n        lint: {}\n"
	writeSetupFixtureStreams(t, manifest.Root, catalog)
	id := finishFixturePreparation(t, ctx, e, projectRecord, project, manifest)
	catalogID := assertQueuedSetupCatalog(t, ctx, e, project, id, qualityRoot)
	finishFixtureCatalogProbe(t, ctx, e, project, catalogID, catalog)
	assertQueuedSetupQualification(t, ctx, e, project.ID, catalogID)
}

func writeSetupFixtureStreams(t *testing.T, root, catalog string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"000.stdout": catalog, "000.stderr": ""} {
		if err := os.WriteFile(filepath.Join(root, "streams", name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func finishFixturePreparation(t *testing.T, ctx context.Context, e *Engine, projectRecord model.Record, project model.Project, manifest verification.JobManifest) string {
	t.Helper()
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
	return id
}

func assertQueuedSetupCatalog(t *testing.T, ctx context.Context, e *Engine, project model.Project, id, qualityRoot string) string {
	t.Helper()
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
	for _, path := range []string{setupFixtureQltyConfig, setupFixtureGitleaksConfig} {
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
	return catalogID
}

func finishFixtureCatalogProbe(t *testing.T, ctx context.Context, e *Engine, project model.Project, catalogID, catalog string) {
	t.Helper()
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
	writeSetupFixtureStreams(t, catalogManifest.Root, catalog)
	catalogEvidence := projectSetupEvidence{Onboarding: true, Phase: "catalog", QualityRoot: catalogInput.QualityRoot, SnapshotHash: catalogInput.SnapshotHash, Manifest: catalogManifest}
	catalogInput.Manifest = catalogManifest
	catalogInput.Onboarding, catalogInput.Phase = true, "catalog"
	catalogOperationValue.Stage, catalogOperationValue.Owner, catalogOperationValue.Input = "running", e.owner, catalogInput
	requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", catalogID, catalogOperationValue, catalogOperation.Version))
	catalogOperationRecord, err := e.Store.Get(ctx, "operations", catalogID)
	requireWorkflowSuccess(t, err)
	catalogOperationValue, err = store.Decode[model.Operation](catalogOperationRecord)
	requireWorkflowSuccess(t, err)
	projectRecord, err := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	savedProject, err := store.Decode[model.Project](projectRecord)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.finishProjectSetupCatalogProbe(ctx, catalogOperationRecord, catalogOperationValue, projectRecord, savedProject, catalogEvidence))
}

func assertQueuedSetupQualification(t *testing.T, ctx context.Context, e *Engine, projectID, catalogID string) {
	t.Helper()
	operations, err := e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	for _, item := range operations {
		candidate, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if item.ID != catalogID && candidate.Kind == "project_setup" && candidate.Subject == projectID && candidate.Stage == "queued" {
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
		Quality:  &model.QualitySnapshot{Root: filepath.Join(root, "quality"), Files: map[string]string{setupFixtureQltyConfig: "config_version = \"0\"\n"}, PluginCatalog: []byte("plugins: {}\n")},
		Commands: []verification.JobCommand{{Name: "build", Command: []string{"/usr/bin/true"}, TimeoutSeconds: 10}, {Name: "quality", Command: []string{"qlty", "check", "--sarif"}, TimeoutSeconds: 10, ReportFormat: "sarif"}},
	}
	identity := &model.QualityIdentity{Version: 1, QltyVersion: "0.668.0 macos-arm64 (fixture)", QltyBinarySHA256: strings.Repeat("a", 64), ConfigurationHash: strings.Repeat("b", 64), ToolRuntimeHash: strings.Repeat("c", 64), PluginCatalogHash: strings.Repeat("d", 64), ToolRuntimePath: filepath.Join(root, "runtime"), Tools: []model.QualityToolIdentity{{Name: "gitleaks", Version: "1.0", Fingerprint: strings.Repeat("e", 8)}}}
	zero, one := 0, 1
	manifestHash := manifest.Digest()
	report := verification.JobReport{Purpose: manifest.Purpose, ProjectID: manifest.ProjectID, OperationID: manifest.OperationID, ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree, Passed: false, QualityIdentity: identity, Results: []verification.JobResult{
		{Result: verification.Result{Name: "build", Command: manifest.Commands[0].Command, ExitCode: &zero, Passed: true, Runtime: verification.RuntimeIdentity{CleanupVerified: true}}, Status: "pass", ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree},
		{Result: verification.Result{Name: "quality", Command: manifest.Commands[1].Command, ExitCode: &one, Passed: false, QualityIdentity: identity, Runtime: verification.RuntimeIdentity{CleanupVerified: true}, Error: "exit status 1"}, Status: "fail", ManifestHash: manifestHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree},
	}}
	if err := os.MkdirAll(filepath.Join(manifest.Root, "streams"), 0700); err != nil {
		t.Fatal(err)
	}
	sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"results":[{"ruleId":"gitleaks:fixture","level":"warning","message":{"text":"existing finding"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"app.py"},"region":{"startLine":1,"snippet":{"text":"eval(value)"}}}}]}],"invocations":[{"executionSuccessful":true,"toolExecutionNotifications":[],"toolConfigurationNotifications":[]}]}]}`
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

func commitSetupFixturePolicy(t *testing.T, repository, configuration string) {
	t.Helper()
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(repository, setupFixturePolicyFile), []byte(configuration), 0600))
	for _, args := range [][]string{{"add", setupFixturePolicyFile}, {"-c", setupFixtureDisableHooks, "commit", "-m", setupFixtureCommitSubject}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf(setupFixtureCommitError, err, output)
		}
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
