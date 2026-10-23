package workflow

import (
	"context"
	"fmt"
	"path"
	"strings"
)

const unsafeSubmissionPath = "unsafe submission path: %q"

func validateSubmissionGroups(groups []CommitGroup, changed []string) error {
	if len(groups) == 0 || len(changed) == 0 {
		return fmt.Errorf("submission requires nonempty commit groups and reviewed changes")
	}
	owners, err := submissionGroupOwners(groups, changed)
	if err != nil {
		return err
	}
	return validateSubmissionTransitions(owners)
}

func submissionGroupOwners(groups []CommitGroup, changed []string) (map[string]int, error) {
	remaining := make(map[string]bool, len(changed))
	for _, file := range changed {
		remaining[file] = true
	}
	owners := make(map[string]int, len(changed))
	for index, group := range groups {
		if strings.TrimSpace(group.Message) == "" || strings.ContainsRune(group.Message, '\x00') || len(group.Files) == 0 {
			return nil, fmt.Errorf("submission groups require a message and files")
		}
		for _, file := range group.Files {
			if err := validateSubmissionPath(file); err != nil {
				return nil, err
			}
			if !remaining[file] {
				return nil, fmt.Errorf("duplicate or unreviewed submission path: %q", file)
			}
			owners[file] = index
			delete(remaining, file)
		}
	}
	if len(remaining) != 0 {
		return nil, fmt.Errorf("submission groups omit %d reviewed paths", len(remaining))
	}
	return owners, nil
}

func validateSubmissionPath(file string) error {
	if file == "" || file == "." || path.IsAbs(file) || path.Clean(file) != file {
		return fmt.Errorf(unsafeSubmissionPath, file)
	}
	if strings.ContainsAny(file, "\\\x00") || strings.HasPrefix(file, "../") {
		return fmt.Errorf(unsafeSubmissionPath, file)
	}
	for _, part := range strings.Split(file, "/") {
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf(unsafeSubmissionPath, file)
		}
	}
	return nil
}

// A restore of an old file path now containing a directory also restores
// descendants. Keep such transitions together so groups cannot overlap.
func validateSubmissionTransitions(owners map[string]int) error {
	for file, owner := range owners {
		for parent := path.Dir(file); parent != "."; parent = path.Dir(parent) {
			if other, exists := owners[parent]; exists && owner != other {
				return fmt.Errorf("file/directory transition must share a commit group: %q", parent)
			}
		}
	}
	return nil
}

func validateSubmissionWorktree(ctx context.Context, source, target, branch string) error {
	if err := validateSubmissionRoot(ctx, target); err != nil {
		return err
	}
	if err := validateSubmissionRepository(ctx, source, target); err != nil {
		return err
	}
	ref, err := submissionGitRun(ctx, target, "symbolic-ref", submissionHead)
	if err != nil || ref != submissionHeads+branch {
		return fmt.Errorf("submission target is not on expected branch")
	}
	return submissionClean(ctx, target)
}

func validateSubmissionRoot(ctx context.Context, target string) error {
	root, err := submissionGitRun(ctx, target, submissionRevParse, "--show-toplevel")
	if err != nil {
		return err
	}
	root, err = submissionPath(root)
	if err != nil || root != target {
		return fmt.Errorf("submission target is not an exact worktree root")
	}
	return nil
}

func validateSubmissionRepository(ctx context.Context, source, target string) error {
	sourceCommon, err := submissionCommonDirectory(ctx, source)
	if err != nil {
		return err
	}
	targetCommon, err := submissionCommonDirectory(ctx, target)
	if err != nil {
		return err
	}
	if sourceCommon != targetCommon {
		return fmt.Errorf("submission target belongs to a different repository")
	}
	return nil
}

func validateSubmissionPrefix(ctx context.Context, target, base, reviewed string, commits []string, groups []CommitGroup) error {
	parent := base
	for i, commit := range commits {
		if err := validateSubmissionCommit(ctx, target, parent, commit, reviewed, groups[i]); err != nil {
			return err
		}
		parent = commit
	}
	return nil
}

func validateSubmissionCommit(ctx context.Context, target, parent, commit, reviewed string, group CommitGroup) error {
	if err := validateSubmissionCommitMetadata(ctx, target, parent, commit, group); err != nil {
		return err
	}
	files, err := submissionChanged(ctx, target, parent, commit)
	if err != nil {
		return err
	}
	if err := validateSubmissionGroups([]CommitGroup{group}, files); err != nil {
		return fmt.Errorf("submission target group differs: %w", err)
	}
	args := append([]string{"diff", "--quiet", reviewed, commit, "--"}, group.Files...)
	if _, err := submissionGitRun(ctx, target, args...); err != nil {
		return fmt.Errorf("submission target group differs from reviewed content: %w", err)
	}
	return nil
}

func validateSubmissionCommitMetadata(ctx context.Context, target, parent, commit string, group CommitGroup) error {
	parents, err := submissionGitRun(ctx, target, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return err
	}
	if parents != commit+" "+parent {
		return fmt.Errorf("submission target has unexpected parent history")
	}
	message, err := submissionGitRun(ctx, target, "show", "-s", "--format=%B", commit)
	if err != nil {
		return err
	}
	if message != strings.TrimRight(group.Message, "\r\n") {
		return fmt.Errorf("submission target commit does not match expected group")
	}
	return nil
}
