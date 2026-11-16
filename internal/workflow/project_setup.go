package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
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
		evidence.Manifest = manifest
		op.Stage, op.Owner, op.ConfigurationHash, op.RepositoryRevision = "running", e.owner, manifest.ConfigurationHash, manifest.CandidateCommit
		op.Input = evidence
		changes := []store.Change{store.VersionPrecondition("projects", project.ID, projectRecord.Version), {Kind: "operations", ID: id, Value: op, Expected: record.Version}}
		if err := e.Store.Write(ctx, changes, nil); err != nil {
			return err
		}
		record.Version++
		binary, err := localVerifierBinary()
		failure = err
		if failure == nil {
			evidence.Report, failure = verification.RunLocalJob(ctx, binary, manifest, func(event verification.JobEvent) error {
				if event.Index < 0 || event.Index >= len(manifest.Commands) {
					return errors.New("project setup progress command index is invalid")
				}
				name := manifest.Commands[event.Index].Name
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
				return e.persistProjectSetupProgress(ctx, &record, op, evidence)
			})
			if failure == nil && !evidence.Report.Passed && !(evidence.Onboarding && evidence.Phase == "qualify" && projectSetupAllowsExistingQltyFindings(manifest, evidence.Report)) {
				failure = errors.New("required project verification command failed; inspect retained setup report")
			}
		}
	}
	if failure == nil && evidence.Onboarding && evidence.Phase != "qualify" {
		if evidence.Phase == "catalog" {
			return e.finishProjectSetupCatalogProbe(ctx, record, op, projectRecord, project, evidence)
		}
		return e.finishProjectSetupPreparation(ctx, record, op, projectRecord, project, evidence)
	}
	return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, failure)
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
			if command.ReportFormat != "sarif" || result.Status != "fail" || result.ExitCode == nil || *result.ExitCode != 1 || result.Error != "" && result.Error != "exit status 1" || !result.Runtime.CleanupVerified || result.QualityIdentity == nil || result.QualityIdentity.Validate() != nil {
				return false
			}
			output, _, err := verification.ReadJobStreams(manifest.Root, index)
			if err != nil || verification.ValidateJobSARIF([]byte(output)) != nil {
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
		runID := "project_setup:" + record.ID
		e.mu.Lock()
		if _, active := e.running[runID]; active || e.closing {
			e.mu.Unlock()
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		e.running[runID] = cancel
		e.background.Go(func() {
			defer func() {
				e.mu.Lock()
				delete(e.running, runID)
				e.mu.Unlock()
			}()
			if err := e.runProjectSetup(runCtx, record.ID); err != nil && !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrLeased) {
				log.Printf("project setup %s: %v", record.ID, err)
			}
		})
		e.mu.Unlock()
	}
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
	if evidence.Onboarding && evidence.Phase != "" && evidence.Phase != "catalog" && evidence.Phase != "qualify" {
		return verification.JobManifest{}, errors.New("unknown durable project setup phase; explicitly retry setup")
	}
	rules, hash, err := policy.Load(project.Path)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if hash != project.PolicyHash {
		return verification.JobManifest{}, errors.New("project policy changed; confirm current configuration before setup")
	}
	head, err := github.Run(ctx, project.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return verification.JobManifest{}, err
	}
	tree, err := github.Run(ctx, project.Path, "git", "rev-parse", head+"^{tree}")
	if err != nil {
		return verification.JobManifest{}, err
	}
	profile, err := execution.ForPolicy(project.Path, true, hash, rules.Sandbox)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if !execution.IsNonoLauncher(profile.Launcher) {
		return verification.JobManifest{}, errors.New("project setup requires native nono")
	}
	manifest := verification.JobManifest{Version: 1, Purpose: verification.ProjectSetupPurpose, ProjectID: project.ID, OperationID: id, JobID: id, ConfigurationHash: hash, CandidateCommit: head, CandidateTree: tree, BaseCommit: head, Repository: project.Path, Root: filepath.Join(e.Root, "project_setup", id), Launcher: profile.Launcher, Sandbox: rules.Sandbox}
	if evidence.Onboarding && evidence.Phase == "" {
		if rules.Sandbox == nil || !slices.Contains(rules.Sandbox.AllowedDomains, "semgrep.dev") {
			return verification.JobManifest{}, errors.New("project setup needs network access to semgrep.dev; add it to committed sandbox.allowed_domains, confirm the project policy, then retry setup")
		}
		qualityPolicy := project.Policy.Quality
		if qualityPolicy == nil {
			qualityPolicy = &model.QualityPolicy{}
		}
		qualityRoot, err := externalQualityRootLocation(project, qualityPolicy)
		if err != nil {
			return verification.JobManifest{}, err
		}
		manifest.Quality = &model.QualitySnapshot{Root: qualityRoot, Files: map[string]string{".qlty/qlty.toml": "config_version = \"0\"\n[[source]]\nname = \"default\"\ndefault = true\n"}}
		manifest.Root = filepath.Join(qualityRoot, "jobs", id)
		manifest.Commands = []verification.JobCommand{
			{Name: "qlty-catalog", Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 30},
			{Name: "semgrep-default-rules", Command: []string{"curl", "-fsSL", "https://semgrep.dev/c/p/ci"}, TimeoutSeconds: 60},
		}
		manifest.ConfigurationHash = evidenceDigest(manifest.Quality)
		return manifest, manifest.Validate()
	}
	if evidence.Onboarding && evidence.Phase == "catalog" {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return verification.JobManifest{}, err
		}
		if snapshot.Root != evidence.QualityRoot || evidenceDigest(snapshot) != evidence.SnapshotHash {
			return verification.JobManifest{}, errors.New("prepared external Qlty configuration changed before catalog probe; retry project setup")
		}
		manifest.Quality = &snapshot
		manifest.Root = filepath.Join(snapshot.Root, "jobs", id)
		manifest.ConfigurationHash = evidenceDigest(snapshot)
		manifest.Commands = []verification.JobCommand{{Name: "qlty-effective-catalog", Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 30}}
		return manifest, manifest.Validate()
	}
	if evidence.Onboarding && evidence.Phase == "qualify" {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return verification.JobManifest{}, err
		}
		if snapshot.Root != evidence.QualityRoot || evidenceDigest(snapshot) != evidence.SnapshotHash {
			return verification.JobManifest{}, errors.New("prepared external Qlty configuration changed before qualification; retry project setup")
		}
		priorRecord, err := e.Store.Get(ctx, "operations", evidence.PreviousOperationID)
		if err != nil {
			return verification.JobManifest{}, fmt.Errorf("load retained Qlty setup catalog: %w", err)
		}
		priorOperation, err := store.Decode[model.Operation](priorRecord)
		if err != nil {
			return verification.JobManifest{}, err
		}
		if priorOperation.Kind != "project_setup" || priorOperation.Subject != project.ID || priorOperation.Stage != "complete" {
			return verification.JobManifest{}, errors.New("effective Qlty catalog checkpoint is not complete for this project; retry project setup")
		}
		priorEvidence := setupEvidenceFromInput(priorOperation.Input)
		if !priorEvidence.Onboarding || priorEvidence.Phase != "catalog" || priorEvidence.Manifest.OperationID != priorRecord.ID {
			return verification.JobManifest{}, errors.New("effective Qlty catalog checkpoint has invalid setup authority; retry project setup")
		}
		catalog, _, err := verification.ReadJobStreams(priorEvidence.Manifest.Root, 0)
		if err != nil {
			return verification.JobManifest{}, fmt.Errorf("read retained Qlty plugin catalog: %w", err)
		}
		catalogHash, err := verification.HashQltyPluginCatalog([]byte(catalog))
		if err != nil || catalogHash != evidence.PluginCatalogHash {
			return verification.JobManifest{}, errors.New("retained Qlty plugin catalog is invalid or changed; retry project setup")
		}
		snapshot.PluginCatalog = []byte(catalog)
		snapshot.Identity = nil
		manifest.Quality = &snapshot
		manifest.Root = filepath.Join(snapshot.Root, "jobs", id)
		manifest.ConfigurationHash = evidenceDigest(snapshot)
	} else if externalQualityConfigured(project) {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return verification.JobManifest{}, err
		}
		manifest.Quality = &snapshot
		manifest.Root = filepath.Join(snapshot.Root, "jobs", id)
		manifest.ConfigurationHash = evidenceDigest(snapshot)
	}
	checks := rules.Checks
	if evidence.Onboarding && evidence.Phase == "qualify" {
		checks = append(append([]model.Check(nil), checks...), model.Check{Name: "qlty-qualification", Mode: "autofix", Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}, TimeoutSeconds: 600, ReportFormat: "sarif"})
	}
	for _, check := range checks {
		timeout := check.TimeoutSeconds
		if timeout <= 0 {
			timeout = 600
		}
		manifest.Commands = append(manifest.Commands, verification.JobCommand{Name: check.Name, Mode: check.Mode, Command: check.Command, TimeoutSeconds: timeout, DependsOn: check.DependsOn, Required: check.Required, OutputPaths: check.OutputPaths, ReportFormat: check.ReportFormat, Environment: check.Environment})
	}
	return manifest, manifest.Validate()
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
	rules, _, err := verification.ReadJobStreams(evidence.Manifest.Root, 1)
	if err != nil {
		return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, fmt.Errorf("read retained Semgrep rules: %w", err))
	}
	_, err = evidence.Manifest.PrepareQualityDefaults([]byte(catalog), []byte(rules))
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
	if failure == nil && (externalQualityConfigured(project) || evidence.Onboarding) {
		if evidence.Report.QualityIdentity == nil || evidence.Report.QualityIdentity.Validate() != nil {
			failure = errors.New("Qlty setup completed without a valid frozen scanner runtime identity; retry project setup")
		} else {
			project.QualityIdentity = evidence.Report.QualityIdentity
		}
	}
	if failure == nil {
		_, hash, err := policy.Load(project.Path)
		if err != nil || hash != project.PolicyHash {
			failure = errors.New("project policy changed during verification; setup result rejected")
		}
	}
	op.Input, op.FinishedAt, op.Owner = evidence, time.Now().UTC().Format(time.RFC3339Nano), e.owner
	op.Stage, op.Error, project.Status = "complete", "", "approved"
	if failure != nil {
		op.Stage, op.Error, project.Status = "failed", failure.Error(), "error"
	}
	if ctx.Err() != nil {
		op.Stage, op.Error, op.FinishedAt, project.Status = "queued", "", "", "draft"
	}
	changes := []store.Change{{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version}, {Kind: "projects", ID: project.ID, Value: project, Expected: projectRecord.Version}}
	if err := e.Store.Write(finish, changes, nil); err != nil {
		return fmt.Errorf("persist project setup: %w", err)
	}
	return nil
}
