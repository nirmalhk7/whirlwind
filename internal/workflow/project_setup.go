package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type projectSetupEvidence struct {
	Manifest verification.JobManifest `json:"manifest"`
	Report   verification.JobReport   `json:"report"`
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
	evidence := projectSetupEvidence{}
	manifest, failure := e.projectSetupManifest(ctx, project, id)
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
			evidence.Report, failure = verification.RunLocalJob(ctx, binary, manifest, nil)
			if failure == nil && !evidence.Report.Passed {
				failure = errors.New("required project verification command failed; inspect retained setup report")
			}
		}
	}
	return e.finishProjectSetup(ctx, record, op, projectRecord, project, evidence, failure)
}

func (e *Engine) projectSetupManifest(ctx context.Context, project model.Project, id string) (verification.JobManifest, error) {
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
	if externalQualityConfigured(project) {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return verification.JobManifest{}, err
		}
		manifest.Quality = &snapshot
		manifest.Root = filepath.Join(snapshot.Root, "jobs", id)
		manifest.ConfigurationHash = evidenceDigest(snapshot)
	}
	for _, check := range rules.Checks {
		timeout := check.TimeoutSeconds
		if timeout <= 0 {
			timeout = 600
		}
		manifest.Commands = append(manifest.Commands, verification.JobCommand{Name: check.Name, Mode: check.Mode, Command: check.Command, TimeoutSeconds: timeout, DependsOn: check.DependsOn, Required: check.Required, OutputPaths: check.OutputPaths, ReportFormat: check.ReportFormat, Environment: check.Environment})
	}
	return manifest, manifest.Validate()
}

func (e *Engine) finishProjectSetup(ctx context.Context, record model.Record, op model.Operation, projectRecord model.Record, project model.Project, evidence projectSetupEvidence, failure error) error {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
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
