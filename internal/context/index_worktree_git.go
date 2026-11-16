package context

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
)

func workingTreeChanges(ctx context.Context, cwd string) ([]string, error) {
	tracked, err := gitOutput(ctx, cwd, "diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("list tracked working-tree changes: %w", err)
	}
	untracked, err := gitOutput(ctx, cwd, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list untracked working-tree sources: %w", err)
	}
	return uniquePaths(append(splitGitPaths(tracked), splitGitPaths(untracked)...)), nil
}

func gitOutput(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func splitGitPaths(value []byte) []string {
	paths := []string{}
	for _, path := range bytes.Split(value, []byte{0}) {
		if len(path) > 0 {
			paths = append(paths, string(path))
		}
	}
	return paths
}

func uniquePaths(paths []string) []string {
	sort.Strings(paths)
	return slices.Compact(paths)
}

func ignoredGitPaths(ctx context.Context, cwd string, paths []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(paths) == 0 {
		return ignored, nil
	}
	input := []byte(strings.Join(paths, "\x00") + "\x00")
	command := exec.CommandContext(ctx, "git", "-C", cwd, "check-ignore", "--no-index", "-z", "--stdin")
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("check ignored source paths: %w", err)
		}
	}
	for _, path := range splitGitPaths(output) {
		ignored[path] = true
	}
	return ignored, nil
}
