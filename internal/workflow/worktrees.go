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
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, err := github.Run(ctx, repo, "git", "worktree", "add", "-b", branch, path, base)
	return err
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
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if _, err := github.Run(ctx, p.Path, "git", "fetch", "--no-tags", "origin", p.DefaultBranch); err != nil {
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
