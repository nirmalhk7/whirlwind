package httpapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"whirlwind/internal/github"
)

func preflightStarterCommit(ctx context.Context, path string) error {
	gitDir, err := github.Run(ctx, path, "git", "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	if err := checkStarterGitState(gitDir); err != nil {
		return err
	}
	unmerged, err := github.Run(ctx, path, "git", "ls-files", "--unmerged")
	if err != nil {
		return err
	}
	if unmerged != "" {
		return errors.New("resolve Git conflicts before creating Whirlwind policy")
	}
	return checkStarterGitIdentity(ctx, path)
}

func checkStarterGitState(gitDir string) error {
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		if _, err := os.Stat(filepath.Join(gitDir, state)); err == nil {
			return errors.New("finish current Git merge, rebase, cherry-pick or revert before creating Whirlwind policy")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func checkStarterGitIdentity(ctx context.Context, path string) error {
	for _, identity := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := github.Run(ctx, path, "git", "var", identity); err != nil {
			return fmt.Errorf("configure Git user.name and user.email before registration: %w", err)
		}
	}
	return nil
}

func commitStarterPolicy(ctx context.Context, path, content string) error {
	configAdded, err := requiredQltyCommit(ctx, path)
	if err != nil {
		return err
	}
	files := []string{generatedPolicyName}
	if configAdded {
		files = append(files, ".qlty/qlty.toml")
	}
	if _, err = github.Run(ctx, path, "git", append([]string{"add", "--"}, files...)...); err != nil {
		return err
	}
	// --only excludes unrelated staged files and preserves their index entries.
	if _, err = github.Run(ctx, path, "git", append([]string{"commit", "--only", "-m", "chore: initialize Whirlwind execution policy", "--"}, files...)...); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if configAdded {
			if _, headErr := github.Run(cleanupCtx, path, "git", "cat-file", "-e", "HEAD:.qlty/qlty.toml"); headErr != nil {
				_, cleanupErr := github.Run(cleanupCtx, path, "git", "rm", "--cached", "--", ".qlty/qlty.toml")
				err = errors.Join(err, cleanupErr)
			}
		}
		return fmt.Errorf("commit generated execution policy before registration: %w", err)
	}
	return verifyStarterCommit(ctx, path, content)
}

func verifyStarterCommit(ctx context.Context, path, content string) error {
	committed, err := github.Run(ctx, path, "git", "show", "HEAD:.whirlwind.yml")
	if err != nil {
		return err
	}
	local, err := os.ReadFile(filepath.Join(path, generatedPolicyName))
	if err != nil {
		return err
	}
	if committed != strings.TrimRight(content, "\r\n") || string(local) != content {
		return errors.New("new .whirlwind.yml changed during commit; review policy before registration")
	}
	return nil
}

// Clean up only the unchanged file this attempt created, allowing a safe retry.
func cleanupUncommittedStarter(ctx context.Context, path, content string) error {
	committed, err := starterPolicyCommitted(ctx, path)
	if err != nil || committed {
		return err
	}
	exists, err := unchangedStarterPolicy(path, content)
	if err != nil || !exists {
		return err
	}
	if err := unstageStarterPolicy(ctx, path); err != nil {
		return err
	}
	return os.Remove(filepath.Join(path, generatedPolicyName))
}

func unchangedStarterPolicy(path, content string) (bool, error) {
	local, err := os.ReadFile(filepath.Join(path, generatedPolicyName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(local) != content {
		return false, errors.New("generated policy changed during failed commit; preserve it and resolve manually before retrying")
	}
	return true, nil
}

func starterPolicyCommitted(ctx context.Context, path string) (bool, error) {
	if _, err := github.Run(ctx, path, "git", "cat-file", "-e", "HEAD:.whirlwind.yml"); err == nil {
		return true, nil
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 128 {
			return false, fmt.Errorf("cannot confirm generated policy is uncommitted: %w", err)
		}
	}
	return false, nil
}

func unstageStarterPolicy(ctx context.Context, path string) error {
	if _, err := github.Run(ctx, path, "git", "ls-files", "--error-unmatch", "--", generatedPolicyName); err == nil {
		if _, err = github.Run(ctx, path, "git", "rm", "--cached", "--", generatedPolicyName); err != nil {
			return fmt.Errorf("cannot unstage generated policy after failed commit: %w", err)
		}
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("cannot confirm generated policy index state: %w", err)
		}
	}
	return nil
}
