package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (step *taskStep) reviewerWorktree(commit, perspective string) (string, error) {
	ctx := step.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	source, err := submissionPath(step.task.Worktree)
	if err != nil {
		return "", err
	}
	commit, err = submissionCommit(ctx, source, commit)
	if err != nil {
		return "", err
	}
	head, err := submissionCommit(ctx, source, "HEAD")
	if err != nil {
		return "", err
	}
	if head != commit {
		return "", errors.New("review candidate changed before its snapshot was created")
	}
	if err := submissionClean(ctx, source); err != nil {
		return "", errors.New("review candidate must be clean before creating reviewer snapshots")
	}
	id := digest([]any{step.task.ID, step.task.PlanVersion, commit, perspective})
	path := filepath.Join(filepath.Dir(source), ".whirlwind-review", id)
	metadata := model.ReviewerWorktree{ID: id, TaskID: step.task.ID, SourceWorktree: source, Worktree: path, Commit: commit, Perspective: perspective, PlanVersion: step.task.PlanVersion}
	record, err := step.Store.Get(ctx, "review_worktrees", id)
	if err == nil {
		saved, decodeErr := store.Decode[model.ReviewerWorktree](record)
		if decodeErr != nil {
			return "", decodeErr
		}
		if saved != metadata {
			return "", store.ErrConflict
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	} else if err := step.Store.Put(ctx, "review_worktrees", id, metadata, 0); err != nil {
		return "", err
	}
	if err := ensureReviewerSnapshot(ctx, source, path, commit); err != nil {
		return "", err
	}
	return path, nil
}

func ensureReviewerSnapshot(ctx context.Context, source, target, commit string) error {
	if target == source || strings.HasPrefix(target, source+string(filepath.Separator)) || strings.HasPrefix(source, target+string(filepath.Separator)) {
		return errors.New("reviewer snapshot must use a distinct worktree")
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		_, err := submissionGitRun(ctx, source, "worktree", "add", "--detach", target, commit)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return validateReviewerSnapshot(ctx, source, target, commit)
}

func validateReviewerSnapshot(ctx context.Context, source, target, commit string) error {
	top, err := submissionGitRun(ctx, target, "rev-parse", "--show-toplevel")
	if err != nil || top != target {
		return errors.New("reviewer snapshot path is occupied by another directory")
	}
	commonSource, err := gitCommonDirectory(ctx, source)
	if err != nil {
		return err
	}
	commonTarget, err := gitCommonDirectory(ctx, target)
	if err != nil || commonSource != commonTarget {
		return errors.New("reviewer snapshot belongs to a different Git repository")
	}
	head, err := submissionCommit(ctx, target, "HEAD")
	if err != nil || head != commit {
		return errors.New("reviewer snapshot does not match the approved candidate commit")
	}
	if _, err := submissionGitRun(ctx, target, "symbolic-ref", "-q", "HEAD"); err == nil {
		return errors.New("reviewer snapshot must remain detached")
	}
	return submissionClean(ctx, target)
}

func gitCommonDirectory(ctx context.Context, path string) (string, error) {
	common, err := submissionGitRun(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(path, common)
	}
	return filepath.EvalSymlinks(common)
}
