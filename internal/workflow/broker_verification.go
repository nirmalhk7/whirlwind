package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

const (
	brokerNoHooksArgument = "core.hooksPath=/dev/null"
)

func (a *agentAttempt) runIndependentBrokerCheck(ctx context.Context, source, head string, check model.Check) (verification.Result, error) {
	plan := a.task
	// Unit execution deliberately omits the mutable orchestration document.
	// Recover only its immutable approved version, never current agent input.
	r, err := a.Store.Get(ctx, "plan_versions", fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion))
	if err != nil {
		return verification.Result{}, err
	}
	frozen, err := store.Decode[model.PlanSpecification](r)
	if err != nil {
		return verification.Result{}, err
	}
	plan.Plan = &frozen
	if err := a.validateApprovalConfiguration(ctx, plan); err != nil {
		return verification.Result{}, err
	}
	plan.Worktree, plan.VerificationID, plan.LastAttemptID = source, "", a.id
	run, err := a.startVerificationAt(ctx, a.project, &plan, head)
	if err != nil {
		return verification.Result{}, err
	}
	checks, err := brokerCheckCommands(a.project.Policy, check.Name)
	if err != nil {
		return verification.Result{}, err
	}
	report, runErr := run.runLocalChecks(a.project.Policy, checks, nil)
	run.value["report"], run.value["stage"], run.value["finished_at"] = report, "complete", time.Now().UTC().Format(time.RFC3339Nano)
	if runErr != nil {
		run.value["stage"], run.value["error"] = "failed", runErr.Error()
	}
	if err := run.save(); err != nil {
		return verification.Result{}, errors.Join(runErr, err)
	}
	if runErr != nil {
		return verification.Result{}, runErr
	}
	if len(report.Results) != len(checks) {
		return verification.Result{}, errors.New("broker verifier returned incomplete command report")
	}
	for _, result := range report.Results {
		if result.Name == check.Name {
			return result, nil
		}
	}
	return verification.Result{}, errors.New("broker verifier omitted requested command")
}

func (a *agentAttempt) runIndependentCodingCheck(ctx context.Context, check model.Check, fingerprint string, files map[string]string) (verification.Result, error) {
	identity := evidenceDigest([]any{a.task.ID, a.task.PlanVersion, a.input.UnitID, fingerprint})
	root := filepath.Join(a.Root, "check-snapshots")
	if err := os.MkdirAll(root, 0700); err != nil {
		return verification.Result{}, err
	}
	source := filepath.Join(root, identity)
	a.gitMu.Lock()
	err := a.freezeCodingCheck(ctx, root, source, fingerprint, files)
	a.gitMu.Unlock()
	if err != nil {
		return verification.Result{}, err
	}
	head, err := submissionCommit(ctx, source, "HEAD")
	if err != nil {
		return verification.Result{}, err
	}
	return a.runIndependentBrokerCheck(ctx, source, head, check)
}

// Copy into a private repository before committing the dirty snapshot. The
// operator's files, original Git index, refs and object database stay untouched.
func (a *agentAttempt) freezeCodingCheck(ctx context.Context, root, target, fingerprint string, files map[string]string) error {
	if _, err := os.Stat(target); err == nil {
		actual, _, _, err := codingSnapshot(ctx, target)
		if err != nil || actual != fingerprint {
			return errors.New("durable Coding check snapshot changed")
		}
		return submissionClean(ctx, target)
	} else if !os.IsNotExist(err) {
		return err
	}
	scratch, err := os.MkdirTemp(root, ".freeze-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	stage := filepath.Join(scratch, "source")
	if _, err := submissionGitRun(ctx, scratch, "-c", brokerNoHooksArgument, "clone", "--no-hardlinks", "--", a.input.CWD, stage); err != nil {
		return err
	}
	head, err := submissionCommit(ctx, a.input.CWD, "HEAD")
	if err != nil {
		return err
	}
	if _, err := submissionGitRun(ctx, stage, "-c", brokerNoHooksArgument, "checkout", "--detach", head); err != nil {
		return err
	}
	if err := a.copyCodingSnapshot(stage, files); err != nil {
		return err
	}
	actual, _, _, err := codingSnapshot(ctx, stage)
	if err != nil || actual != fingerprint {
		return errors.New("source changed while freezing Coding check snapshot")
	}
	if _, err := submissionGitRun(ctx, stage, "add", "-A", "--", "."); err != nil {
		return err
	}
	if _, err := submissionGitRun(ctx, stage, "-c", brokerNoHooksArgument, "-c", "user.name=Whirlwind", "-c", "user.email=whirlwind@localhost", "commit", "--allow-empty", "--no-gpg-sign", "--no-verify", "-m", "Frozen Coding verification snapshot"); err != nil {
		return err
	}
	return os.Rename(stage, target)
}

func (a *agentAttempt) copyCodingSnapshot(stage string, files map[string]string) error {
	source, err := os.OpenRoot(a.input.CWD)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = destination.Close() }()
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := destination.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	for path, value := range files {
		if err := copyCodingSnapshotEntry(source, destination, path, value); err != nil {
			return err
		}
	}
	return nil
}

func copyCodingSnapshotEntry(source, destination *os.Root, path, value string) error {
	if !filepath.IsLocal(path) || path == ".git" {
		return errors.New("unsafe Coding check snapshot path")
	}
	if value == "deleted" {
		return nil
	}
	file, info, err := openCodingSnapshotFile(source, path)
	if err != nil {
		return err
	}
	if file == nil {
		return errors.New("source changed while copying Coding check snapshot")
	}
	defer func() { _ = file.Close() }()
	if err := destination.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	target, err := destination.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, file)
	return errors.Join(copyErr, target.Close())
}
