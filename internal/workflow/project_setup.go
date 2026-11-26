package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type projectSetupEvidence struct {
	Manifest            verification.JobManifest `json:"manifest"`
	Report              verification.JobReport   `json:"report"`
	Progress            projectSetupProgress     `json:"progress"`
	Onboarding          bool                     `json:"onboarding,omitempty"`
	Phase               string                   `json:"phase,omitempty"`
	QualityRoot         string                   `json:"quality_root,omitempty"`
	SnapshotHash        string                   `json:"snapshot_hash,omitempty"`
	PreviousOperationID string                   `json:"previous_operation_id,omitempty"`
	PluginCatalogHash   string                   `json:"plugin_catalog_hash,omitempty"`
}

type projectSetupProgress struct {
	Current   string              `json:"current,omitempty"`
	Completed []projectSetupCheck `json:"completed,omitempty"`
}

type projectSetupCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// runProjectSetup uses the same durable operation identity as its independent
// verification job. A setup result never borrows an approved plan's authority.
func (e *Engine) runProjectSetup(ctx context.Context, id string) error {
	record, err := e.Store.Get(ctx, "operations", id)
	if err != nil {
		return err
	}
	op, err := store.Decode[model.Operation](record)
	if err != nil || op.Kind != "project_setup" || op.Stage != "queued" {
		return err
	}
	ctx, release, err := e.projectRunnerContext(ctx, op.Subject)
	if err != nil {
		return err
	}
	defer release()
	key := "project_setup:" + op.Subject
	lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
	ctx = store.WithLeaseFence(ctx, key, lease)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.renewLease(ctx, cancel, key, lease.Fence, nil)
	projectRecord, err := e.Store.Get(ctx, "projects", op.Subject)
	if err != nil {
		return err
	}
	project, err := store.Decode[model.Project](projectRecord)
	if err != nil || project.Paused || project.Status == "complete" {
		return err
	}
	evidence := setupEvidenceFromInput(op.Input)
	manifest, failure := e.projectSetupManifest(ctx, project, id, evidence)
	if failure == nil {
		if manifest.OfflineDockerImage != "" {
			evidence.Onboarding = false
		}
		evidence.Manifest = manifest
		op.Stage, op.Owner, op.ConfigurationHash, op.RepositoryRevision = "running", e.owner, manifest.ConfigurationHash, manifest.CandidateCommit
		op.Input = evidence
		changes := []store.Change{store.VersionPrecondition("projects", project.ID, projectRecord.Version), {Kind: "operations", ID: id, Value: op, Expected: record.Version}}
		if err := e.Store.Write(ctx, changes, nil); err != nil {
			return err
		}
		record.Version++
		failure = e.runProjectSetupVerifier(ctx, &record, op, &evidence)
	}
	return e.finishSetupOperation(ctx, pendingSetupCompletion{record: record, operation: op, projectRecord: projectRecord, project: project, evidence: evidence, failure: failure})
}

type pendingSetupCompletion struct {
	record, projectRecord model.Record
	operation             model.Operation
	project               model.Project
	evidence              projectSetupEvidence
	failure               error
}

func (e *Engine) finishSetupOperation(ctx context.Context, setup pendingSetupCompletion) error {
	record, op, projectRecord, project := setup.record, setup.operation, setup.projectRecord, setup.project
	evidence, failure := setup.evidence, setup.failure
	if failure == nil && evidence.Onboarding && evidence.Phase != "qualify" {
		if evidence.Phase == "catalog" {
			return e.finishProjectSetupCatalogProbe(ctx, record, op, projectRecord, project, evidence)
		}
		return e.finishProjectSetupPreparation(ctx, record, op, projectRecord, project, evidence)
	}
	return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, failure)
}

func (e *Engine) runProjectSetupVerifier(ctx context.Context, record *model.Record, op model.Operation, evidence *projectSetupEvidence) error {
	binary, err := localVerifierBinary()
	if err != nil {
		return err
	}
	manifest := evidence.Manifest
	evidence.Report, err = verification.RunLocalJob(ctx, binary, manifest, func(event verification.JobEvent) error {
		if err := evidence.applyProgress(event); err != nil {
			return err
		}
		return e.persistProjectSetupProgress(ctx, record, op, *evidence)
	})
	return projectSetupReportError(*evidence, err)
}

func projectSetupReportError(evidence projectSetupEvidence, err error) error {
	if err != nil || evidence.Report.Passed {
		return err
	}
	if evidence.Onboarding && evidence.Phase == "qualify" && projectSetupAllowsExistingQltyFindings(evidence.Manifest, evidence.Report) {
		return nil
	}
	return errors.New("required project verification command failed; inspect retained setup report")
}

func (evidence *projectSetupEvidence) applyProgress(event verification.JobEvent) error {
	commands := evidence.Manifest.Commands
	if event.Index < 0 || event.Index >= len(commands) {
		return errors.New("project setup progress command index is invalid")
	}
	name := commands[event.Index].Name
	switch event.Type {
	case "started":
		evidence.Progress.Current = name
	case "result":
		if event.Result == nil || event.Result.Name != name {
			return errors.New("project setup progress result is incomplete")
		}
		evidence.Progress.Current = ""
		evidence.Progress.Completed = append(evidence.Progress.Completed, projectSetupCheck{Name: name, Status: event.Result.Status, Error: event.Result.Error})
	}
	return nil
}

func projectSetupAllowsExistingQltyFindings(manifest verification.JobManifest, report verification.JobReport) bool {
	if manifest.Purpose != verification.ProjectSetupPurpose || report.Passed || report.Validate(manifest) != nil {
		return false
	}
	foundQltyFindings := false
	for index, command := range manifest.Commands {
		result := report.Results[index]
		isQltyCheck := len(command.Command) > 1 && filepath.Base(command.Command[0]) == "qlty" && command.Command[1] == "check"
		if isQltyCheck {
			if !validProjectSetupQltyFinding(manifest.Root, index, command, result) {
				return false
			}
			foundQltyFindings = true
			continue
		}
		if (command.Required == nil || *command.Required) && result.Status != "pass" {
			return false
		}
	}
	return foundQltyFindings && report.QualityIdentity != nil && report.QualityIdentity.Validate() == nil
}

func validProjectSetupQltyFinding(root string, index int, command verification.JobCommand, result verification.JobResult) bool {
	if command.ReportFormat != "sarif" || result.Status != "fail" || result.ExitCode == nil || *result.ExitCode != 1 || result.Error != "" && result.Error != "exit status 1" || !result.Runtime.CleanupVerified || result.QualityIdentity == nil || result.QualityIdentity.Validate() != nil {
		return false
	}
	output, _, err := verification.ReadJobStreams(root, index)
	return err == nil && verification.ValidateJobSARIF([]byte(output)) == nil
}

func (e *Engine) dispatchProjectSetup(ctx context.Context) {
	if e.settings(ctx).Stopped {
		return
	}
	records, err := e.Store.List(ctx, "operations")
	if err != nil {
		return
	}
	for _, record := range records {
		operation, err := store.Decode[model.Operation](record)
		if err != nil || operation.Kind != "project_setup" || operation.Stage != "queued" {
			continue
		}
		project, err := e.Project(ctx, operation.Subject)
		if err != nil || project.Paused || project.Status == "complete" {
			continue
		}
		e.launchProjectSetup(ctx, record.ID)
	}
}

func (e *Engine) launchProjectSetup(ctx context.Context, operationID string) {
	runID := "project_setup:" + operationID
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, active := e.running[runID]; active || e.closing {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.running[runID] = cancel
	e.background.Go(func() {
		defer func() {
			e.mu.Lock()
			delete(e.running, runID)
			e.mu.Unlock()
		}()
		if err := e.runProjectSetup(runCtx, operationID); err != nil && !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrLeased) {
			log.Printf("project setup %s: %v", operationID, err)
		}
	})
}

func (e *Engine) persistProjectSetupProgress(ctx context.Context, record *model.Record, op model.Operation, evidence projectSetupEvidence) error {
	current, err := e.Store.Get(ctx, "operations", record.ID)
	if err != nil {
		return err
	}
	if current.Version != record.Version {
		return store.ErrConflict
	}
	latest, err := store.Decode[model.Operation](current)
	if err != nil {
		return err
	}
	if latest.Owner != e.owner || latest.Stage != "running" {
		return store.ErrConflict
	}
	op.Input = evidence
	if err := e.Store.Put(ctx, "operations", record.ID, op, current.Version); err != nil {
		return err
	}
	record.Version++
	return nil
}

func (e *Engine) projectSetupManifest(ctx context.Context, project model.Project, id string, evidence projectSetupEvidence) (verification.JobManifest, error) {
	if evidence.Onboarding && !validOnboardingPhase(evidence.Phase) {
		return verification.JobManifest{}, errors.New("unknown durable project setup phase; explicitly retry setup")
	}
	rules, err := e.projectPolicy(ctx, project)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if project.ManagedPolicy {
		project.Policy = rules
	}
	manifest, err := e.frozenProjectSetupManifest(ctx, project, id)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if rules.OfflineDockerImage != "" {
		return offlineProjectSetupManifest(manifest, project, rules)
	}
	return e.nativeProjectSetupManifest(ctx, manifest, project, rules, evidence)
}

func validOnboardingPhase(phase string) bool {
	switch phase {
	case "", "catalog", "qualify":
		return true
	default:
		return false
	}
}

func (e *Engine) frozenProjectSetupManifest(ctx context.Context, project model.Project, id string) (verification.JobManifest, error) {
	head, err := github.Run(ctx, project.Path, "git", gitRevParseCommand, "HEAD")
	if err != nil {
		return verification.JobManifest{}, err
	}
	tree, err := github.Run(ctx, project.Path, "git", gitRevParseCommand, head+"^{tree}")
	if err != nil {
		return verification.JobManifest{}, err
	}
	return verification.JobManifest{
		Version: 1, Purpose: verification.ProjectSetupPurpose, ProjectID: project.ID, OperationID: id, JobID: id,
		ConfigurationHash: project.PolicyHash, CandidateCommit: head, CandidateTree: tree, BaseCommit: head,
		Repository: project.Path, Root: filepath.Join(e.Root, "project_setup", id),
	}, nil
}

func offlineProjectSetupManifest(manifest verification.JobManifest, project model.Project, rules model.Policy) (verification.JobManifest, error) {
	if !project.ManagedPolicy {
		return verification.JobManifest{}, errors.New("offline setup requires approved external policy")
	}
	manifest.OfflineDockerImage, manifest.PolicyHash = rules.OfflineDockerImage, project.PolicyHash
	manifest.Commands = verificationJobCommands(rules.Checks)
	manifest.BindManagedOfflineQlty()
	return manifest, manifest.Validate()
}

func (e *Engine) nativeProjectSetupManifest(ctx context.Context, manifest verification.JobManifest, project model.Project, rules model.Policy, evidence projectSetupEvidence) (verification.JobManifest, error) {
	profile, err := execution.ForPolicy(project.Path, true, project.PolicyHash, rules.Sandbox)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if !execution.IsNonoLauncher(profile.Launcher) {
		return verification.JobManifest{}, errors.New("project setup requires native nono")
	}
	manifest.Launcher, manifest.Sandbox = profile.Launcher, rules.Sandbox
	if evidence.Onboarding {
		return e.onboardingSetupManifest(ctx, manifest, project, rules.Checks, evidence)
	}
	if externalQualityConfigured(project) {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return verification.JobManifest{}, err
		}
		applySetupQualitySnapshot(&manifest, snapshot)
	}
	manifest.Commands = verificationJobCommands(rules.Checks)
	manifest.BindManagedOfflineQlty()
	return manifest, manifest.Validate()
}

func (e *Engine) onboardingSetupManifest(ctx context.Context, manifest verification.JobManifest, project model.Project, checks []model.Check, evidence projectSetupEvidence) (verification.JobManifest, error) {
	if evidence.Phase == "" {
		return planSetupCatalogProbe(manifest, project)
	}
	snapshot, err := loadPreparedSetupSnapshot(project, evidence)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if evidence.Phase == "catalog" {
		return setupCatalogProbe(manifest, snapshot, "qlty-effective-catalog")
	}
	catalog, err := e.retainedSetupCatalog(ctx, project.ID, evidence)
	if err != nil {
		return verification.JobManifest{}, err
	}
	snapshot.PluginCatalog, snapshot.Identity = catalog, nil
	applySetupQualitySnapshot(&manifest, snapshot)
	checks = append(append([]model.Check(nil), checks...), model.Check{Name: "qlty-qualification", Mode: "autofix", Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}, TimeoutSeconds: 600, ReportFormat: "sarif"})
	manifest.Commands = verificationJobCommands(checks)
	return manifest, manifest.Validate()
}

func loadPreparedSetupSnapshot(project model.Project, evidence projectSetupEvidence) (model.QualitySnapshot, error) {
	snapshot, err := externalQualitySnapshot(project)
	if err != nil {
		return model.QualitySnapshot{}, err
	}
	if setupSnapshotMatches(snapshot, evidence) {
		return snapshot, nil
	}
	if evidence.Phase == "catalog" {
		return model.QualitySnapshot{}, errors.New("prepared external Qlty configuration changed before catalog probe; retry project setup")
	}
	return model.QualitySnapshot{}, errors.New("prepared external Qlty configuration changed before qualification; retry project setup")
}

func planSetupCatalogProbe(manifest verification.JobManifest, project model.Project) (verification.JobManifest, error) {
	qualityPolicy := project.Policy.Quality
	if qualityPolicy == nil {
		qualityPolicy = &model.QualityPolicy{}
	}
	root, err := externalQualityRootLocation(project, qualityPolicy)
	if err != nil {
		return verification.JobManifest{}, err
	}
	snapshot := model.QualitySnapshot{Root: root, Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n[[source]]\nname = \"default\"\ndefault = true\n"}}
	return setupCatalogProbe(manifest, snapshot, "qlty-catalog")
}

func setupCatalogProbe(manifest verification.JobManifest, snapshot model.QualitySnapshot, name string) (verification.JobManifest, error) {
	applySetupQualitySnapshot(&manifest, snapshot)
	manifest.Commands = []verification.JobCommand{
		{Name: name, Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 30},
	}
	return manifest, manifest.Validate()
}

func applySetupQualitySnapshot(manifest *verification.JobManifest, snapshot model.QualitySnapshot) {
	manifest.Quality = &snapshot
	manifest.Root = filepath.Join(snapshot.Root, "jobs", manifest.JobID)
	manifest.ConfigurationHash = evidenceDigest(snapshot)
}

func setupSnapshotMatches(snapshot model.QualitySnapshot, evidence projectSetupEvidence) bool {
	return snapshot.Root == evidence.QualityRoot && evidenceDigest(snapshot) == evidence.SnapshotHash
}

func (e *Engine) retainedSetupCatalog(ctx context.Context, projectID string, evidence projectSetupEvidence) ([]byte, error) {
	priorRecord, err := e.Store.Get(ctx, "operations", evidence.PreviousOperationID)
	if err != nil {
		return nil, fmt.Errorf("load retained Qlty setup catalog: %w", err)
	}
	priorOperation, err := store.Decode[model.Operation](priorRecord)
	if err != nil {
		return nil, err
	}
	if priorOperation.Kind != "project_setup" || priorOperation.Subject != projectID || priorOperation.Stage != "complete" {
		return nil, errors.New("effective Qlty catalog checkpoint is not complete for this project; retry project setup")
	}
	priorEvidence := setupEvidenceFromInput(priorOperation.Input)
	if !priorEvidence.Onboarding || priorEvidence.Phase != "catalog" || priorEvidence.Manifest.OperationID != priorRecord.ID {
		return nil, errors.New("effective Qlty catalog checkpoint has invalid setup authority; retry project setup")
	}
	return readSetupCatalog(priorEvidence.Manifest.Root, evidence.PluginCatalogHash)
}

func readSetupCatalog(root, expectedHash string) ([]byte, error) {
	catalog, _, err := verification.ReadJobStreams(root, 0)
	if err != nil {
		return nil, fmt.Errorf("read retained Qlty plugin catalog: %w", err)
	}
	data := []byte(catalog)
	catalogHash, err := verification.HashQltyPluginCatalog(data)
	if err != nil || catalogHash != expectedHash {
		return nil, errors.New("retained Qlty plugin catalog is invalid or changed; retry project setup")
	}
	return data, nil
}

func setupEvidenceFromInput(value any) projectSetupEvidence {
	raw, err := json.Marshal(value)
	if err != nil {
		return projectSetupEvidence{}
	}
	var evidence projectSetupEvidence
	if json.Unmarshal(raw, &evidence) == nil && (evidence.Onboarding || evidence.Manifest.Purpose == verification.ProjectSetupPurpose) {
		return evidence
	}
	var marker struct {
		Onboarding bool `json:"onboarding"`
	}
	_ = json.Unmarshal(raw, &marker)
	evidence.Onboarding = marker.Onboarding
	return evidence
}

func (e *Engine) finishProjectSetupPreparation(ctx context.Context, record model.Record, op model.Operation, projectRecord model.Record, project model.Project, evidence projectSetupEvidence) error {
	catalog, _, err := verification.ReadJobStreams(evidence.Manifest.Root, 0)
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("read retained Qlty catalog: %w", err))
	}
	_, err = evidence.Manifest.PrepareQualityDefaults([]byte(catalog))
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("prepare external Qlty defaults: %w", err))
	}
	snapshot, err := externalQualitySnapshot(project)
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("read prepared external Qlty defaults: %w", err))
	}
	nextID := store.ID()
	next := projectSetupEvidence{Onboarding: true, Phase: "catalog", QualityRoot: snapshot.Root, SnapshotHash: evidenceDigest(snapshot)}
	operation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: next}
	op.Input, op.Stage, op.Owner = evidence, "complete", e.owner
	op.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	project.Status = "draft"
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := e.Store.Write(finish, []store.Change{{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version}, {Kind: "operations", ID: nextID, Value: operation}, {Kind: "projects", ID: project.ID, Value: project, Expected: projectRecord.Version}}, nil); err != nil {
		return fmt.Errorf("persist Qlty setup checkpoint: %w", err)
	}
	e.Wake()
	return nil
}

func (e *Engine) finishProjectSetupCatalogProbe(ctx context.Context, record model.Record, op model.Operation, projectRecord model.Record, project model.Project, evidence projectSetupEvidence) error {
	catalog, _, err := verification.ReadJobStreams(evidence.Manifest.Root, 0)
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("read effective Qlty plugin catalog: %w", err))
	}
	catalogHash, err := verification.HashQltyPluginCatalog([]byte(catalog))
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("validate effective Qlty plugin catalog: %w", err))
	}
	nextID := store.ID()
	next := projectSetupEvidence{Onboarding: true, Phase: "qualify", QualityRoot: evidence.QualityRoot, SnapshotHash: evidence.SnapshotHash, PreviousOperationID: record.ID, PluginCatalogHash: catalogHash}
	operation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: next}
	op.Input, op.Stage, op.Owner = evidence, "complete", e.owner
	op.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	project.Status = "draft"
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := e.Store.Write(finish, []store.Change{{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version}, {Kind: "operations", ID: nextID, Value: operation}, {Kind: "projects", ID: project.ID, Value: project, Expected: projectRecord.Version}}, nil); err != nil {
		return fmt.Errorf("persist effective Qlty catalog checkpoint: %w", err)
	}
	e.Wake()
	return nil
}

func (e *Engine) finishProjectSetup(ctx context.Context, record model.Record, op model.Operation, projectRecord model.Record, project model.Project, evidence projectSetupEvidence, failure error) error {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if failure == nil {
		failure = e.qualifyProjectSetup(ctx, &project, evidence)
	}
	op.Input, op.FinishedAt, op.Owner = evidence, time.Now().UTC().Format(time.RFC3339Nano), e.owner
	op.Stage, op.Error, project.Status = "complete", "", "approved"
	if failure != nil {
		op.Stage, op.Error, project.Status = "failed", failure.Error(), "error"
	}
	if ctx.Err() != nil {
		op.Stage, op.Error, op.FinishedAt, project.Status = "queued", "", "", "draft"
	}
	if project.Status != "approved" {
		project.OfflineQualification = nil
	}
	changes := []store.Change{{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version}, {Kind: "projects", ID: project.ID, Value: project, Expected: projectRecord.Version}}
	if err := e.Store.Write(finish, changes, nil); err != nil {
		return fmt.Errorf("persist project setup: %w", err)
	}
	return nil
}

func (e *Engine) qualifyProjectSetup(ctx context.Context, project *model.Project, evidence projectSetupEvidence) error {
	if externalQualityConfigured(*project) || evidence.Onboarding {
		identity := evidence.Report.QualityIdentity
		if identity == nil || identity.Validate() != nil {
			return errors.New("setup completed without a valid frozen Qlty scanner runtime identity; retry project setup")
		}
		project.QualityIdentity = identity
	}
	if _, err := e.projectPolicy(ctx, *project); err != nil {
		return errors.New("project policy changed during verification; setup result rejected")
	}
	if evidence.Manifest.OfflineDockerImage != "" {
		var err error
		project.OfflineQualification, err = offlineSetupQualification(ctx, *project, evidence)
		return err
	}
	return nil
}
