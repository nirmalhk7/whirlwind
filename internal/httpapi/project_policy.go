package httpapi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"whirlwind/internal/github"
)

const (
	generatedPolicyName     = ".whirlwind.yml"
	generatedQltyConfigName = ".qlty/qlty.toml"
	generatedCachedFlag     = "--cached"
)

const olderStarterPolicy = `# Empty policy allows ideation only. Configure and commit before executing tasks.
# Fill in checks, allowed_paths and risk_rules; review and commit this file.
# See docs/go-backend.md in Whirlwind for a complete policy example.
checks: []
allowed_paths: []
commands: []
risk_rules: []
`

func generatedLegacyStarter(path string) (string, error) {
	name, err := legacyStarterFile(path)
	if err != nil || name == "" {
		return "", err
	}
	contents, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	if string(contents) == starterPolicy || string(contents) == olderStarterPolicy {
		return string(contents), nil
	}
	return "", nil
}

func upgradeGeneratedStarter(ctx context.Context, path, previous string) error {
	next, err := prepareStarterUpgrade(ctx, path, previous)
	if err != nil {
		return err
	}
	if err = replaceStarterPolicy(path, previous, next); err != nil {
		return err
	}
	if err = commitStarterPolicy(ctx, path, next); err != nil {
		return restoreFailedStarterUpgrade(path, previous, next, err)
	}
	return nil
}

// Execution worktrees need the same Qlty configuration that onboarding validated.
// Commit a missing config, but never consume edits to an existing tracked/staged config.
func requiredQltyCommit(ctx context.Context, path string) (bool, error) {
	if _, err := github.Run(ctx, path, "git", "cat-file", "-e", "HEAD:"+generatedQltyConfigName); err == nil {
		return false, unchangedTrackedQlty(ctx, path)
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 128 {
			return false, err
		}
	}
	staged, err := github.Run(ctx, path, "git", "diff", generatedCachedFlag, "--", generatedQltyConfigName)
	if err != nil {
		return false, err
	}
	if staged != "" {
		return false, errors.New("commit staged Qlty configuration before execution setup; existing staging preserved")
	}
	return true, nil
}

func legacyStarterFile(path string) (string, error) {
	name := filepath.Join(path, generatedPolicyName)
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	if _, err = os.Lstat(filepath.Join(path, ".whirlwind.yaml")); err == nil {
		return "", nil
	}
	return name, nil
}

func prepareStarterUpgrade(ctx context.Context, path, previous string) (string, error) {
	if err := preflightStarterCommit(ctx, path); err != nil {
		return "", err
	}
	if err := unchangedStarterForUpgrade(ctx, path, previous); err != nil {
		return "", err
	}
	if err := initializeProjectTools(ctx, path); err != nil {
		return "", err
	}
	return generatedExecutionPolicy(ctx, path)
}

func replaceStarterPolicy(path, previous, next string) error {
	current, err := os.ReadFile(filepath.Join(path, generatedPolicyName))
	if err != nil {
		return err
	}
	if string(current) != previous {
		return errors.New("starter changed during setup; preserve edits and retry")
	}
	if err = os.WriteFile(filepath.Join(path, generatedPolicyName), []byte(next), 0600); err != nil {
		return err
	}
	return nil
}

func restoreFailedStarterUpgrade(path, previous, next string, err error) error {
	// Restore only our unchanged replacement when the original is still HEAD.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	head, readErr := github.Run(cleanupCtx, path, "git", "show", "HEAD:"+generatedPolicyName)
	local, fileErr := os.ReadFile(filepath.Join(path, generatedPolicyName))
	if readErr == nil && fileErr == nil && head == strings.TrimRight(previous, "\r\n") && string(local) == next {
		restoreErr := os.WriteFile(filepath.Join(path, generatedPolicyName), []byte(previous), 0600)
		_, indexErr := github.Run(cleanupCtx, path, "git", "restore", "--staged", "--", generatedPolicyName)
		return errors.Join(err, restoreErr, indexErr)
	}
	return err
}

func unchangedTrackedQlty(ctx context.Context, path string) error {
	for _, args := range [][]string{{"diff", "--", generatedQltyConfigName}, {"diff", generatedCachedFlag, "--", generatedQltyConfigName}} {
		changes, e := github.Run(ctx, path, "git", args...)
		if e != nil {
			return e
		}
		if changes != "" {
			return errors.New("commit Qlty configuration edits before execution setup; existing edits preserved")
		}
	}
	return nil
}

func unchangedStarterForUpgrade(ctx context.Context, path, previous string) error {
	committed, err := github.Run(ctx, path, "git", "show", "HEAD:"+generatedPolicyName)
	if err != nil || committed != strings.TrimRight(previous, "\r\n") {
		return errors.New("commit unchanged starter policy before upgrading execution setup")
	}
	staged, err := github.Run(ctx, path, "git", "diff", generatedCachedFlag, "--", generatedPolicyName)
	if err != nil {
		return err
	}
	if staged != "" {
		return errors.New("preserve staged policy edits; commit them before execution setup")
	}
	return nil
}
