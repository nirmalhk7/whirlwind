package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"whirlwind/internal/github"
)

const (
	submissionRevParse = "rev-parse"
	submissionHead     = "HEAD"
	submissionHeads    = "refs/heads/"
	submissionTree     = "^{tree}"
)

// CommitGroup describes a host-applied, whole-file portion of a reviewed tree.
type CommitGroup struct {
	Message string   `json:"message"`
	Files   []string `json:"files"`
}

type submissionAssembly struct {
	source, target, branch, base, reviewed string
	groups                                 []CommitGroup
}

// organizeSubmission reconstructs reviewed changes on a separate branch without
// rewriting checkpoint history. Recovery accepts only completed expected groups.
func organizeSubmission(ctx context.Context, sourceWorktree, targetWorktree, branch, base, reviewedCommit string, groups []CommitGroup) (string, error) {
	assembly, err := resolveSubmission(ctx, sourceWorktree, targetWorktree, branch, base, reviewedCommit, groups)
	if err != nil {
		return "", err
	}
	completed, err := assembly.prepare(ctx)
	if err != nil {
		return "", err
	}
	if err := assembly.apply(ctx, completed); err != nil {
		return "", err
	}
	return assembly.finish(ctx)
}

func resolveSubmission(ctx context.Context, source, target, branch, base, reviewed string, groups []CommitGroup) (submissionAssembly, error) {
	a := submissionAssembly{branch: branch, base: base, reviewed: reviewed, groups: groups}
	var err error
	a.source, err = submissionPath(source)
	if err != nil {
		return a, err
	}
	a.target, err = submissionPath(target)
	if err != nil {
		return a, err
	}
	if a.source == a.target || strings.HasPrefix(a.target, a.source+string(filepath.Separator)) || strings.HasPrefix(a.source, a.target+string(filepath.Separator)) {
		return a, fmt.Errorf("submission requires a distinct, non-nested worktree")
	}
	if err := a.resolveReferences(ctx); err != nil {
		return a, err
	}
	return a, nil
}

func (a *submissionAssembly) resolveReferences(ctx context.Context) error {
	if err := submissionSource(ctx, a.source, a.reviewed); err != nil {
		return err
	}
	var err error
	a.base, err = submissionCommit(ctx, a.source, a.base)
	if err != nil {
		return err
	}
	a.reviewed, err = submissionCommit(ctx, a.source, a.reviewed)
	if err != nil {
		return err
	}
	if _, err := submissionGitRun(ctx, a.source, "merge-base", "--is-ancestor", a.base, a.reviewed); err != nil {
		return fmt.Errorf("submission base is not an ancestor of reviewed commit: %w", err)
	}
	return nil
}

func (a *submissionAssembly) prepare(ctx context.Context) (int, error) {
	changed, err := submissionChanged(ctx, a.source, a.base, a.reviewed)
	if err != nil {
		return 0, err
	}
	if err := validateSubmissionGroups(a.groups, changed); err != nil {
		return 0, err
	}
	if err := validateSubmissionBranch(ctx, a.source, a.branch); err != nil {
		return 0, err
	}
	if err := prepareSubmissionWorktree(ctx, a.source, a.target, a.branch, a.base); err != nil {
		return 0, err
	}
	return submissionPrefix(ctx, a.target, a.base, a.reviewed, a.groups)
}

func (a *submissionAssembly) apply(ctx context.Context, completed int) error {
	for _, group := range a.groups[completed:] {
		if err := a.applyGroup(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func (a *submissionAssembly) applyGroup(ctx context.Context, group CommitGroup) error {
	args := []string{"restore", "--source", a.reviewed, "--staged", "--worktree", "--"}
	if _, err := submissionGitRun(ctx, a.target, append(args, group.Files...)...); err != nil {
		return err
	}
	_, err := submissionGitRun(ctx, a.target, "-c", "user.name=Whirlwind", "-c", "user.email=whirlwind@localhost", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "--cleanup=verbatim", "-m", group.Message)
	return err
}

func (a *submissionAssembly) finish(ctx context.Context) (string, error) {
	if err := validateSubmissionTree(ctx, a.source, a.target, a.reviewed); err != nil {
		return "", err
	}
	if err := submissionClean(ctx, a.target); err != nil {
		return "", err
	}
	if err := submissionSource(ctx, a.source, a.reviewed); err != nil {
		return "", err
	}
	return submissionGitRun(ctx, a.target, submissionRevParse, submissionHead)
}

func submissionGitRun(ctx context.Context, dir string, args ...string) (string, error) {
	return github.Run(ctx, dir, "git", append([]string{"--literal-pathspecs"}, args...)...)
}

func submissionCommit(ctx context.Context, dir, revision string) (string, error) {
	return submissionGitRun(ctx, dir, submissionRevParse, "--verify", "--end-of-options", revision+"^{commit}")
}

// Resolve symlinked existing parents as well as worktrees not created yet.
func submissionPath(value string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent, err := submissionPath(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func submissionClean(ctx context.Context, dir string) error {
	status, err := submissionGitRun(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("submission worktree must be clean: %s", dir)
	}
	return nil
}

func submissionSource(ctx context.Context, source, reviewed string) error {
	head, err := submissionGitRun(ctx, source, submissionRevParse, submissionHead)
	if err != nil {
		return err
	}
	want, err := submissionCommit(ctx, source, reviewed)
	if err != nil {
		return err
	}
	if head != want {
		return fmt.Errorf("source HEAD differs from reviewed commit")
	}
	return submissionClean(ctx, source)
}

func submissionChanged(ctx context.Context, dir, from, to string) ([]string, error) {
	out, err := submissionGitRun(ctx, dir, "diff", "--no-renames", "--name-only", "-z", from, to, "--")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

func prepareSubmissionWorktree(ctx context.Context, source, target, branch, base string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return createSubmissionWorktree(ctx, source, target, branch, base)
	} else if err != nil {
		return err
	}
	return validateSubmissionWorktree(ctx, source, target, branch)
}

func createSubmissionWorktree(ctx context.Context, source, target, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	_, err := submissionGitRun(ctx, source, "worktree", "add", "-b", branch, target, base)
	return err
}

func submissionPrefix(ctx context.Context, target, base, reviewed string, groups []CommitGroup) (int, error) {
	if _, err := submissionGitRun(ctx, target, "merge-base", "--is-ancestor", base, submissionHead); err != nil {
		return 0, fmt.Errorf("submission target does not descend from base: %w", err)
	}
	out, err := submissionGitRun(ctx, target, "rev-list", "--reverse", base+".."+submissionHead)
	if err != nil || out == "" {
		return 0, err
	}
	commits := strings.Split(out, "\n")
	if len(commits) > len(groups) {
		return 0, fmt.Errorf("submission target contains unexpected commits")
	}
	if err := validateSubmissionPrefix(ctx, target, base, reviewed, commits, groups); err != nil {
		return 0, err
	}
	return len(commits), nil
}

func validateSubmissionBranch(ctx context.Context, source, branch string) error {
	if strings.HasPrefix(branch, "-") || branch == "" {
		return fmt.Errorf("invalid submission branch")
	}
	_, err := submissionGitRun(ctx, source, "check-ref-format", submissionHeads+branch)
	return err
}

func validateSubmissionTree(ctx context.Context, source, target, reviewed string) error {
	want, err := submissionGitRun(ctx, source, submissionRevParse, reviewed+submissionTree)
	if err != nil {
		return err
	}
	got, err := submissionGitRun(ctx, target, submissionRevParse, submissionHead+submissionTree)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("submission tree differs from reviewed tree")
	}
	return nil
}

func submissionCommonDirectory(ctx context.Context, dir string) (string, error) {
	common, err := submissionGitRun(ctx, dir, submissionRevParse, "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return submissionPath(common)
}
