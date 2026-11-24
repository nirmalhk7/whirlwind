package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func offlineSetupQualification(ctx context.Context, project model.Project, evidence projectSetupEvidence) (*model.OfflineDockerQualification, error) {
	head, err := github.Run(ctx, project.Path, "git", "rev-parse", "HEAD")
	if err != nil || head != evidence.Manifest.CandidateCommit {
		return nil, errors.New("project source revision changed during offline setup")
	}
	return offlineSetupProof(project, evidence)
}

func offlineSetupProof(project model.Project, evidence projectSetupEvidence) (*model.OfflineDockerQualification, error) {
	m, report := evidence.Manifest, evidence.Report
	if !project.ManagedPolicy || m.Purpose != verification.ProjectSetupPurpose || m.ProjectID != project.ID || m.PolicyHash != project.PolicyHash ||
		m.OfflineDockerImage != project.Policy.OfflineDockerImage || m.Repository != project.Path || !report.Passed {
		return nil, errors.New("offline setup proof does not match the approved external project policy")
	}
	if evidenceDigest(m.Commands) != evidenceDigest(verificationJobCommands(project.Policy.Checks)) {
		return nil, errors.New("offline setup proof does not cover the approved project checks")
	}
	if err := report.Validate(m); err != nil {
		return nil, err
	}
	imageID := report.Results[0].Runtime.ImageID
	for _, result := range report.Results {
		if result.Status != "pass" || result.Runtime.ImageID != imageID || !execution.IsOfflineDockerIdentity(result.Runtime.SandboxID, imageID) || !result.Runtime.CleanupVerified {
			return nil, errors.New("offline setup lacks completed isolation and image evidence for every check")
		}
	}
	return &model.OfflineDockerQualification{PolicyHash: m.PolicyHash, JobID: m.JobID, ImageID: imageID}, nil
}

func (e *Engine) qualifiedOfflineProject(ctx context.Context, project model.Project) (*model.OfflineDockerQualification, error) {
	qualification := project.OfflineQualification
	if !project.ManagedPolicy || project.Status != "approved" || qualification == nil || qualification.PolicyHash != project.PolicyHash {
		return nil, errors.New("complete managed offline project setup before running its approved checks")
	}
	record, err := e.Store.Get(ctx, "operations", qualification.JobID)
	if err != nil {
		return nil, err
	}
	op, err := store.Decode[model.Operation](record)
	if err != nil || op.Kind != "project_setup" || op.Subject != project.ID || op.Stage != "complete" || op.ConfigurationHash != project.PolicyHash {
		return nil, errors.New("managed offline qualification lacks a matching completed setup operation")
	}
	evidence := setupEvidenceFromInput(op.Input)
	if evidence.Manifest.JobID != record.ID || evidence.Manifest.Root != filepath.Join(e.Root, "project_setup", record.ID) || evidence.Manifest.CandidateCommit != op.RepositoryRevision {
		return nil, errors.New("managed offline qualification setup identity mismatch")
	}
	proof, err := offlineSetupProof(project, evidence)
	if err != nil {
		return nil, err
	}
	if *proof != *qualification {
		return nil, errors.New("offline setup proof and qualified image differ")
	}
	return qualification, nil
}

func (run *verificationRun) bindOfflineManifest(m *verification.JobManifest, policy model.Policy) error {
	project, err := run.engine.Project(run.ctx, run.task.ProjectID)
	if err != nil {
		return err
	}
	if policy.OfflineDockerImage != project.Policy.OfflineDockerImage || m.Sandbox != nil || run.task.IdeaID == "" ||
		run.task.Plan == nil || run.task.Plan.Approval == nil || run.task.PlanDocument.PolicyHash != project.PolicyHash {
		return errors.New("offline verification requires a canonical plan approved for the current managed project policy")
	}
	if err := run.engine.validateApprovalConfiguration(run.ctx, *run.task); err != nil {
		return err
	}
	approved := append([]model.Check(nil), project.Policy.Checks...)
	for _, risk := range project.Policy.RiskRules {
		approved = append(approved, risk.Checks...)
	}
	permitted := verificationJobCommands(approved)
	for _, command := range m.Commands {
		identity := evidenceDigest(command)
		if !slices.ContainsFunc(permitted, func(check verification.JobCommand) bool { return evidenceDigest(check) == identity }) {
			return errors.New("offline command differs from the approved project check")
		}
	}
	qualification, err := run.engine.qualifiedOfflineProject(run.ctx, project)
	if err != nil {
		return err
	}
	m.ProjectID, m.PolicyHash = project.ID, project.PolicyHash
	m.OfflineDockerImage, m.OfflineImageID = policy.OfflineDockerImage, qualification.ImageID
	return nil
}

func verificationJobCommands(checks []model.Check) []verification.JobCommand {
	out := make([]verification.JobCommand, 0, len(checks))
	for _, check := range checks {
		timeout := check.TimeoutSeconds
		if timeout <= 0 {
			timeout = 600
		}
		out = append(out, verification.JobCommand{Name: check.Name, Mode: check.Mode, Command: append([]string(nil), check.Command...), TimeoutSeconds: timeout,
			DependsOn: append([]string(nil), check.DependsOn...), Required: check.Required, OutputPaths: append([]string(nil), check.OutputPaths...), ReportFormat: check.ReportFormat, Environment: check.Environment})
	}
	return out
}
