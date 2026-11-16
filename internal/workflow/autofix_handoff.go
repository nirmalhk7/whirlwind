package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func (a *agentRun) prepareAutofixHandoff() error {
	if a.input.Role != "code" || len(a.task.Handoff.AutofixProposals) == 0 {
		return nil
	}
	record, err := a.Store.Get(a.ctx, "execution_plans", a.task.ID)
	if err != nil {
		return err
	}
	plan, err := store.Decode[model.ExecutionPlan](record)
	if err != nil {
		return err
	}
	if plan.ProjectID != a.project.ID || plan.ProjectID != a.task.ProjectID || plan.PlanVersion != a.task.PlanVersion || plan.PlanDocument == nil || plan.PlanDocument.ApprovedHash == "" {
		return errors.New("autofix input requires the current approved project plan")
	}
	a.task.Handoff.AutofixProposals = append([]model.AutofixHandoff{}, a.task.Handoff.AutofixProposals...)
	for i := range a.task.Handoff.AutofixProposals {
		proposal := &a.task.Handoff.AutofixProposals[i]
		content, err := a.validateAutofixInput(plan, *proposal)
		if err != nil {
			return err
		}
		path, err := a.materializeAutofixInput(proposal.ArtifactID, content)
		if err != nil {
			return err
		}
		proposal.Path = path // Never trust a path carried in a handoff.
		a.autofixReadRoots = append(a.autofixReadRoots, path)
	}
	return nil
}

func (a *agentRun) validateAutofixInput(plan model.ExecutionPlan, proposal model.AutofixHandoff) ([]byte, error) {
	if proposal.PlanVersion != plan.PlanVersion || proposal.ArtifactID == "" || proposal.VerificationID == "" {
		return nil, errors.New("autofix input plan version mismatch")
	}
	record, err := a.Store.Get(a.ctx, "verification", proposal.VerificationID)
	if err != nil {
		return nil, err
	}
	source, err := store.Decode[struct {
		Stage      string                   `json:"stage"`
		Provenance model.Provenance         `json:"provenance"`
		Manifest   verification.JobManifest `json:"runner_manifest"`
		Report     verification.JobReport   `json:"runner_report"`
	}](record)
	if err != nil {
		return nil, err
	}
	m := source.Manifest
	if (source.Stage != "complete" && source.Stage != "failed") || m.JobID != proposal.VerificationID || m.PlanID != plan.ID || m.PlanVersion != plan.PlanVersion || m.PlanHash != plan.PlanDocument.ApprovedHash || m.ConfigurationHash != verificationConfigurationHash(plan) || m.CandidateCommit != proposal.CandidateCommit || m.CandidateTree != proposal.CandidateTree || source.Provenance.Task != plan.ID || source.Provenance.PlanVersion != plan.PlanVersion || source.Provenance.Head != proposal.CandidateCommit || source.Provenance.PolicyHash != plan.PlanDocument.PolicyHash {
		return nil, errors.New("autofix input does not match approved plan/candidate evidence")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := source.Report.Validate(m); err != nil {
		return nil, err
	}
	for i, result := range source.Report.Results {
		if result.Name != proposal.CheckID {
			continue
		}
		return a.validatedAutofixArtifact(m.Commands[i], result, proposal)
	}
	return nil, errors.New("autofix command evidence missing")
}

func (a *agentRun) materializeAutofixInput(id string, content []byte) (string, error) {
	scope := sha256.Sum256([]byte(a.task.ID + ":" + fmt.Sprint(a.task.PlanVersion)))
	dir := filepath.Join(a.Root, "agent-inputs", fmt.Sprintf("%x", scope[:]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("autofix input directory is not private")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	key := sha256.Sum256([]byte(id))
	name := fmt.Sprintf("%x.patch", key[:])
	if err := retainAutofixInput(root, name, content); err != nil {
		return "", err
	}
	return filepath.Join(root.Name(), name), nil
}

func (a *agentRun) validatedAutofixArtifact(command verification.JobCommand, result verification.JobResult, proposal model.AutofixHandoff) ([]byte, error) {
	if command.Mode != "autofix" || result.Autofix == nil || result.Autofix.SHA256 != proposal.SHA256 || !runtimeIdentityComplete(result.Runtime) || result.Runtime.Kind != "nono" || result.Status == "error" || result.Status == "timeout" {
		return nil, errors.New("autofix input lacks complete isolated command evidence")
	}
	artifact, content, err := a.Store.Artifact(a.ctx, proposal.ArtifactID)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(content)
	if artifact.OwnerKind != "verification" || artifact.AttemptID != proposal.VerificationID || artifact.Kind != "autofix_patch:"+proposal.CheckID || artifact.Truncated || int64(len(content)) != result.Autofix.Bytes || hex.EncodeToString(hash[:]) != proposal.SHA256 {
		return nil, errors.New("autofix artifact ownership or bytes mismatch")
	}
	return content, nil
}

func retainAutofixInput(root *os.Root, name string, content []byte) error {
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("autofix input path is not a regular file")
		}
		existing, err := root.ReadFile(name)
		if err != nil || !bytes.Equal(existing, content) {
			return errors.New("retained autofix input bytes changed")
		}
	} else if os.IsNotExist(err) {
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(content)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			return err
		}
	} else {
		return err
	}
	return nil
}
