package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

// safe strips unsafe characters from generated path/branch identifiers and substitutes a random ID if nothing remains.
func safe(id string) string {
	var b strings.Builder
	for _, c := range id {
		if safeRune(c) {
			b.WriteRune(c)
		}
	}
	s := b.String()
	if s == "" {
		s = store.ID()
	}
	return s
}

// worktree serializes Git worktree creation and reuses an existing worktree path.
func (e *Engine) worktree(ctx context.Context, repo, path, branch, base string) error {
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("existing worktree path is not a directory owned by this repository: %s", path)
		}
		return validateExistingWorktree(ctx, repo, path, branch, base)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, err := github.Run(ctx, repo, "git", "worktree", "add", "-b", branch, path, base)
	return err
}

func validateExistingWorktree(ctx context.Context, repo, path, branch, base string) error {
	top, err := github.Run(ctx, path, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("existing worktree is not a Git worktree: %w", err)
	}
	wantTop, err := canonicalPath(path)
	if err != nil {
		return err
	}
	actualTop, err := canonicalPath(top)
	if err != nil || actualTop != wantTop {
		return fmt.Errorf("existing worktree root does not match requested path: %s", path)
	}
	wantCommon, err := gitCommonDirectory(ctx, repo)
	if err != nil {
		return err
	}
	actualCommon, err := gitCommonDirectory(ctx, path)
	if err != nil || actualCommon != wantCommon {
		return fmt.Errorf("existing worktree belongs to another repository: %s", path)
	}
	actualBranch, err := github.Run(ctx, path, "git", "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || actualBranch != branch {
		return fmt.Errorf("existing worktree branch does not match requested branch %q: %s", branch, path)
	}
	if _, err := github.Run(ctx, path, "git", "merge-base", "--is-ancestor", base, "HEAD"); err != nil {
		return fmt.Errorf("existing worktree history does not contain requested base %s: %w", base, err)
	}
	registered, err := registeredWorktree(ctx, repo, wantTop, branch)
	if err != nil {
		return err
	}
	if !registered {
		return fmt.Errorf("existing path is not registered as the requested repository worktree: %s", path)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func registeredWorktree(ctx context.Context, repo, target, branch string) (bool, error) {
	list, err := github.Run(ctx, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	for _, entry := range strings.Split(list, "\n\n") {
		lines := strings.Split(entry, "\n")
		registeredPath, registeredBranch := "", ""
		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				registeredPath = strings.TrimPrefix(line, "worktree ")
			}
			if strings.HasPrefix(line, "branch ") {
				registeredBranch = strings.TrimPrefix(line, "branch ")
			}
		}
		canonical, canonicalErr := canonicalPath(registeredPath)
		if canonicalErr == nil && canonical == target && registeredBranch == "refs/heads/"+branch {
			return true, nil
		}
	}
	return false, nil
}

// commit rejects changed paths outside approved scope, stages changes, and commits nonempty diffs with Whirlwind identity.
func (e *Engine) commit(ctx context.Context, p model.Project, path, message string) error {
	files, err := github.Run(ctx, path, "git", "status", "--porcelain", "-uall")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(files, "\n") {
		if len(line) < 4 {
			continue
		}
		f := strings.TrimSpace(line[3:])
		if strings.Contains(f, " -> ") {
			f = strings.Split(f, " -> ")[1]
		}
		f = strings.Trim(f, "\"")
		if !policy.AllowedPath(p.Policy, f) {
			return fmt.Errorf("out-of-scope file: %s", f)
		}
	}
	if _, err = github.Run(ctx, path, "git", "add", "--all"); err != nil {
		return err
	}
	diff, err := github.Run(ctx, path, "git", "diff", "--cached", "--name-only")
	if err != nil || diff == "" {
		return err
	}
	_, err = github.Run(ctx, path, "git", "-c", "user.name=Whirlwind", "-c", "user.email=whirlwind@localhost", "commit", "-m", message)
	return err
}

// base fetches and resolves the configured remote default branch, or uses local HEAD when no default branch is recorded.
func (e *Engine) base(ctx context.Context, p model.Project) (string, error) {
	if p.DefaultBranch == "" {
		return github.Run(ctx, p.Path, "git", "rev-parse", "HEAD")
	}
	remotes, err := github.Run(ctx, p.Path, "git", "remote")
	if err != nil {
		return "", err
	}
	hasOrigin := false
	for _, remote := range strings.Fields(remotes) {
		if remote == "origin" {
			hasOrigin = true
			break
		}
	}
	if !hasOrigin {
		ref := "refs/heads/" + p.DefaultBranch + "^{commit}"
		commit, err := github.Run(ctx, p.Path, "git", "rev-parse", "--verify", ref)
		if err != nil {
			return "", fmt.Errorf("local default branch %q is unavailable and no origin remote is configured: %w", p.DefaultBranch, err)
		}
		return commit, nil
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if _, err := github.Read(ctx, p.Path, "git", "fetch", "--no-tags", "origin", p.DefaultBranch); err != nil {
		return "", err
	}
	return github.Run(ctx, p.Path, "git", "rev-parse", "refs/remotes/origin/"+p.DefaultBranch)
}

func safeRune(c rune) bool {
	switch {
	case c == '-' || c == '_':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	}
	return c >= '0' && c <= '9'
}
