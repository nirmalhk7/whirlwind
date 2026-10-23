package workflow

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// runCheck accepts a committed ID only; reviewers never supply a command or cwd.
func (a *agentAttempt) runCheck(ctx context.Context, id string) (provider.CheckResult, error) {
	if a.input.Role == "code" {
		return a.runCodingCheck(ctx, id)
	}
	if !a.input.ReadOnly || a.input.Role != "review" {
		return provider.CheckResult{}, errors.New("check broker requires read-only reviewer role")
	}
	check, err := committedCheck(a.project.Policy, id)
	if err != nil {
		return provider.CheckResult{}, err
	}
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	current, err := a.trustedProject(ctx, a.project.ID)
	if err != nil {
		return provider.CheckResult{}, err
	}
	if current.PolicyHash != a.project.PolicyHash {
		return provider.CheckResult{}, errors.New("check policy changed")
	}
	head, err := github.Run(ctx, a.input.CWD, "git", "rev-parse", "HEAD")
	if err != nil {
		return provider.CheckResult{}, err
	}
	if a.task.VerificationID == "" {
		return provider.CheckResult{}, errors.New("reviewed revision evidence required")
	}
	record, err := a.Store.Get(ctx, "verification", a.task.VerificationID)
	if err != nil {
		return provider.CheckResult{}, err
	}
	proof, err := store.Decode[struct {
		Provenance model.Provenance `json:"provenance"`
		Stage      string           `json:"stage"`
	}](record)
	if err != nil || proof.Stage != "complete" || proof.Provenance.Head != head || proof.Provenance.PolicyHash != current.PolicyHash || proof.Provenance.Task != a.task.ID || proof.Provenance.PlanVersion != a.task.PlanVersion {
		return provider.CheckResult{}, errors.New("review check requires the exact verified review revision and policy")
	}
	scratch, err := os.MkdirTemp("", "whirlwind-review-check-")
	if err != nil {
		return provider.CheckResult{}, err
	}
	defer os.RemoveAll(scratch)
	snapshot := filepath.Join(scratch, "source")
	if err = os.Mkdir(snapshot, 0700); err != nil {
		return provider.CheckResult{}, err
	}
	cmd := process.Command(ctx, "git", "-C", a.input.CWD, "archive", "--format=tar", head)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return provider.CheckResult{}, err
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err = process.Start(cmd); err != nil {
		return provider.CheckResult{}, err
	}
	extractErr := extractSnapshot(snapshot, pipe)
	if extractErr != nil {
		_ = cmd.Cancel()
	} else {
		var drained int64
		drained, extractErr = io.Copy(io.Discard, io.LimitReader(pipe, 1024*1024+1))
		if drained > 1024*1024 {
			extractErr = errors.New("review archive trailing data exceeds limit")
			_ = cmd.Cancel()
		}
	}
	waitErr := cmd.Wait()
	process.Forget(cmd)
	if err = errors.Join(extractErr, waitErr); err != nil {
		return provider.CheckResult{}, fmt.Errorf("review check snapshot: %w", err)
	}
	profile, err := execution.ForPolicy(snapshot, true, current.PolicyHash, current.Policy.Sandbox)
	if err != nil {
		return provider.CheckResult{}, err
	}
	profile.RuntimeDir = filepath.Join(scratch, "runtime")
	if err = os.Mkdir(profile.RuntimeDir, 0700); err != nil {
		return provider.CheckResult{}, err
	}
	effective, err := execution.ForCheck(profile, check.OutputPaths)
	if err != nil {
		return provider.CheckResult{}, err
	}
	effective, err = execution.ForCommand(effective, check.Command)
	if err != nil {
		return provider.CheckResult{}, err
	}
	report, err := verification.RunObservedWithProfile(ctx, snapshot, []model.Check{check}, profile, nil)
	if err != nil {
		return provider.CheckResult{}, err
	}
	if len(report.Results) != 1 {
		return provider.CheckResult{}, errors.New("check runner returned no result")
	}
	result := report.Results[0]
	artifact, err := a.Store.Capture(ctx, a.id, "review_check:"+id, "text/plain", []byte(result.Output), store.MessagePayloadLimit, false)
	if err != nil {
		return provider.CheckResult{}, a.persistenceError(err)
	}
	output := provider.CheckResult{ExecutionProfile: effective.Digest(), CheckID: id, Head: head, Snapshot: head, Passed: result.Passed, ExitCode: result.ExitCode, Output: string(store.Prefix([]byte(result.Output), 4096)), OutputArtifact: artifact.ID, Truncated: result.Truncated || artifact.Truncated, DurationMS: result.DurationMS}
	if result.Error != "" {
		output.Output += "\n" + result.Error
	}
	value := map[string]any{"execution_profile": effective.Digest(), "task": a.task.ID, "attempt_id": a.id, "plan_version": a.task.PlanVersion, "policy_hash": current.PolicyHash, "head": head, "check_id": id, "result": result, "output_artifact": artifact.ID}
	if err = a.Store.Put(ctx, "review_checks", store.ID(), value, 0); err != nil {
		return output, a.persistenceError(err)
	}
	a.event("whirlwind/review_check", output)
	return output, nil
}

func committedCheck(policy model.Policy, id string) (model.Check, error) {
	if id == "" {
		return model.Check{}, errors.New("committed check ID required")
	}
	checks := append([]model.Check{}, policy.Checks...)
	for _, rule := range policy.RiskRules {
		checks = append(checks, rule.Checks...)
	}
	var found *model.Check
	for _, check := range checks {
		if check.Name != id {
			continue
		}
		if found != nil && !reflect.DeepEqual(*found, check) {
			return model.Check{}, errors.New("ambiguous committed check ID")
		}
		copy := check
		found = &copy
	}
	if found == nil {
		return model.Check{}, fmt.Errorf("unknown committed check ID: %s", id)
	}
	return *found, nil
}

// Reject links rather than allowing archive extraction to follow a path outside the snapshot.
func extractSnapshot(root string, input io.Reader) error {
	reader := tar.NewReader(io.LimitReader(input, 128*1024*1024+1))
	var total int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if count >= 20000 || header.Size < 0 || header.Size > 16*1024*1024 || total+header.Size > 128*1024*1024 {
			return errors.New("review snapshot exceeds limits")
		}
		name := filepath.Clean(header.Name)
		if !filepath.IsLocal(name) || name == ".git" || strings.HasPrefix(name, ".git/") {
			return errors.New("unsafe review snapshot path")
		}
		target := filepath.Join(root, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if err = errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
			continue
		default:
			return errors.New("review snapshot links and special files are unsupported")
		}
		total += header.Size
	}
}
