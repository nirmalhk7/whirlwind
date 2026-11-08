package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// promoteReviewedSnapshot creates a detached finalizer worktree and an
// idempotent per-plan branch reference to the exact accepted commit.
func promoteReviewedSnapshot(ctx context.Context, source, target, branch, base, reviewed string) (string, error) {
	source, err := submissionPath(source)
	if err != nil {
		return "", err
	}
	target, err = submissionPath(target)
	if err != nil {
		return "", err
	}
	return promoteNormalizedSnapshot(ctx, source, target, branch, base, reviewed)
}

func promoteNormalizedSnapshot(ctx context.Context, source, target, branch, base, reviewed string) (string, error) {
	if source == target || strings.HasPrefix(target, source+string(filepath.Separator)) || strings.HasPrefix(source, target+string(filepath.Separator)) {
		return "", errors.New("local delivery requires a distinct worktree")
	}
	if err := submissionClean(ctx, source); err != nil {
		return "", fmt.Errorf("reviewed candidate must be clean: %w", err)
	}
	head, err := submissionCommit(ctx, source, "HEAD")
	if err != nil {
		return "", err
	}
	want, err := submissionCommit(ctx, source, reviewed)
	if err != nil {
		return "", err
	}
	if head != want {
		return "", errors.New("candidate HEAD differs from accepted review snapshot")
	}
	baseCommit, err := submissionCommit(ctx, source, base)
	if err != nil {
		return "", err
	}
	if _, err := submissionGitRun(ctx, source, "merge-base", "--is-ancestor", baseCommit, want); err != nil {
		return "", fmt.Errorf("accepted review base is not an ancestor of candidate: %w", err)
	}
	if err := validateSubmissionBranch(ctx, source, branch); err != nil {
		return "", err
	}
	if err := ensureDeliveryBranch(ctx, source, branch, want); err != nil {
		return "", err
	}
	if err := ensureDetachedDeliveryWorktree(ctx, source, target, want); err != nil {
		return "", err
	}
	if err := validateSubmissionTree(ctx, source, target, want); err != nil {
		return "", err
	}
	return want, nil
}

func ensureDeliveryBranch(ctx context.Context, source, branch, commit string) error {
	ref := submissionHeads + branch
	existing, err := submissionGitRun(ctx, source, "show-ref", "--verify", "--hash", ref)
	if err == nil {
		if existing != commit {
			return fmt.Errorf("delivery branch %q already points to a different commit", branch)
		}
		return nil
	}
	zero, err := emptyObjectID(ctx, source)
	if err != nil {
		return err
	}
	return createDeliveryBranchRef(ctx, source, ref, commit, zero)
}

func createDeliveryBranchRef(ctx context.Context, source, ref, commit, zero string) error {
	if _, err := submissionGitRun(ctx, source, "update-ref", ref, commit, zero); err != nil {
		return fmt.Errorf("create local delivery branch: %w", err)
	}
	return nil
}

func emptyObjectID(ctx context.Context, repo string) (string, error) {
	format, err := submissionGitRun(ctx, repo, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	if format == "sha256" {
		return strings.Repeat("0", 64), nil
	}
	return strings.Repeat("0", 40), nil
}

func ensureDetachedDeliveryWorktree(ctx context.Context, source, target, commit string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return createDetachedDeliveryWorktree(ctx, source, target, commit)
	}
	return validateDetachedDeliveryWorktree(ctx, source, target, commit)
}

func createDetachedDeliveryWorktree(ctx context.Context, source, target, commit string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	_, err := submissionGitRun(ctx, source, "worktree", "add", "--detach", target, commit)
	return err
}

func validateDetachedDeliveryWorktree(ctx context.Context, source, target, commit string) error {
	top, err := submissionGitRun(ctx, target, "rev-parse", "--show-toplevel")
	if err != nil || top != target {
		return errors.New("delivery worktree path is occupied by another directory")
	}
	commonSource, err := gitCommonDirectory(ctx, source)
	if err != nil {
		return err
	}
	commonTarget, err := gitCommonDirectory(ctx, target)
	if err != nil || commonSource != commonTarget {
		return errors.New("existing delivery worktree belongs to a different Git repository")
	}
	head, err := submissionGitRun(ctx, target, "rev-parse", "HEAD")
	if err != nil || head != commit {
		return errors.New("existing delivery worktree does not match accepted commit")
	}
	if _, err := submissionGitRun(ctx, target, "symbolic-ref", "-q", "HEAD"); err == nil {
		return errors.New("delivery worktree must remain detached")
	}
	return submissionClean(ctx, target)
}
