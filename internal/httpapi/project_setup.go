package httpapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"whirlwind/internal/github"
)

// Legacy app-generated starter, recognized exactly for an explicit execution upgrade.
const starterPolicy = `# Whirlwind starter policy: ideation only.
# Configure checks, allowed_paths and risk_rules before enabling task execution.
checks: []
allowed_paths: []
commands: []
risk_rules: []
`

func initializeProject(ctx context.Context, path string) error {
	exists, err := existingProjectPolicy(ctx, path)
	if err != nil {
		return err
	}
	if exists {
		return initializeExistingProject(ctx, path)
	}
	if err = preflightStarterCommit(ctx, path); err != nil {
		return err
	}
	return initializeStarterPolicy(ctx, path, false)
}

func initializeExistingProject(ctx context.Context, path string) error {
	old, err := generatedLegacyStarter(path)
	if err != nil {
		return err
	}
	if old != "" {
		return upgradeGeneratedStarter(ctx, path, old)
	}
	return initializeStarterPolicy(ctx, path, true)
}

func initializeStarterPolicy(ctx context.Context, path string, exists bool) error {
	if err := initializeProjectTools(ctx, path); err != nil {
		return err
	}
	content := ""
	var err error
	if !exists {
		content, err = generatedExecutionPolicy(ctx, path)
		if err != nil {
			return err
		}
	}
	created, err := createStarterPolicy(ctx, path, content)
	if err != nil || !created {
		return err
	}
	if err = commitStarterPolicy(ctx, path, content); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanupErr := cleanupUncommittedStarter(cleanupCtx, path, content)
		return errors.Join(err, cleanupErr)
	}
	return nil
}

func initializeProjectTools(ctx context.Context, path string) error {
	for _, tool := range []projectSetupTool{
		{"qlty", ".qlty/qlty.toml", []string{"init", "--yes", "--no-upgrade-check"}},
		{"codegraph", ".codegraph/codegraph.db", []string{"init"}},
	} {
		if err := tool.initialize(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

type projectSetupTool struct {
	name, marker string
	args         []string
}

func (tool projectSetupTool) initialize(ctx context.Context, path string) error {
	configured, err := projectToolConfigured(path, tool.marker)
	if err != nil {
		return fmt.Errorf("%s configuration: %w", tool.name, err)
	}
	if !configured || tool.name == "qlty" {
		if _, err = exec.LookPath(tool.name); err != nil {
			return fmt.Errorf("%s required for project setup; install it and retry registration: %w", tool.name, err)
		}
	}
	if !configured {
		if err := tool.createConfiguration(ctx, path); err != nil {
			return err
		}
	}
	if tool.name == "qlty" {
		if _, err = github.Run(ctx, path, "qlty", "config", "validate", "--no-upgrade-check"); err != nil {
			return fmt.Errorf("qlty configuration invalid; fix .qlty/qlty.toml and retry registration: %w", err)
		}
	}
	return nil
}

func (tool projectSetupTool) createConfiguration(ctx context.Context, path string) error {
	if _, err := github.Run(ctx, path, tool.name, tool.args...); err != nil {
		return fmt.Errorf("initialize %s before registration: %w", tool.name, err)
	}
	configured, err := projectToolConfigured(path, tool.marker)
	if err != nil {
		return fmt.Errorf("%s configuration: %w", tool.name, err)
	}
	if !configured {
		return fmt.Errorf("%s initialization did not create %s; fix setup and retry registration", tool.name, tool.marker)
	}
	return nil
}

func projectToolConfigured(path, marker string) (bool, error) {
	info, err := os.Stat(filepath.Join(path, marker))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return false, fmt.Errorf("%s must be a nonempty regular file", marker)
	}
	return true, nil
}

func createStarterPolicy(ctx context.Context, path, content string) (bool, error) {
	exists, err := existingProjectPolicy(ctx, path)
	if err != nil || exists {
		return false, err
	}
	file, err := os.OpenFile(filepath.Join(path, ".whirlwind.yml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = file.WriteString(content)
	closeErr := file.Close()
	return true, errors.Join(err, closeErr)
}

func existingProjectPolicy(ctx context.Context, path string) (bool, error) {
	for _, name := range []string{".whirlwind.yml", ".whirlwind.yaml"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		// A staged policy missing from the checkout must also remain untouched.
		if exec.CommandContext(ctx, "git", "-C", path, "ls-files", "--error-unmatch", "--", name).Run() == nil {
			return true, nil
		}
		// A missing checkout of an existing committed policy must not be replaced.
		if exec.CommandContext(ctx, "git", "-C", path, "cat-file", "-e", "HEAD:"+name).Run() == nil {
			return true, nil
		}
	}
	return false, nil
}
