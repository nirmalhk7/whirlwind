package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

// ideationWorktree creates an isolated read-only agent checkout pinned to the
// discovery revision. Each role/attempt receives its own path.
func (e *Engine) ideationWorktree(ctx context.Context, project model.Project, revision, runID, role string, attempt int) (string, func(), error) {
	if strings.TrimSpace(revision) == "" {
		return "", nil, errors.New("ideation snapshot has no pinned Git revision")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(role) == "" {
		return "", nil, errors.New("ideation worktree requires run and role identities")
	}
	path := filepath.Join(e.Root, "ideation", safe(project.ID), safe(runID), fmt.Sprintf("%s-%02d", role, attempt))
	e.gitMu.Lock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		e.gitMu.Unlock()
		return "", nil, err
	}
	_, err := github.Run(ctx, project.Path, "git", "worktree", "add", "--detach", path, revision)
	e.gitMu.Unlock()
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		e.gitMu.Lock()
		_, _ = github.Run(context.Background(), project.Path, "git", "worktree", "remove", "--force", path)
		e.gitMu.Unlock()
	}
	return path, cleanup, nil
}
