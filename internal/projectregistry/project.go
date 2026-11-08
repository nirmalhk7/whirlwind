package projectregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/workflow"
)

func Root(ctx context.Context, path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	topLevel, err := github.Run(ctx, canonical, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(topLevel)
}

func Load(ctx context.Context, name, inputPath string) (model.Project, error) {
	path, err := Root(ctx, inputPath)
	if err != nil {
		return model.Project{}, err
	}
	repo, remoteURL, err := repositoryIdentity(ctx, path)
	if err != nil {
		return model.Project{}, err
	}
	branch := repositoryBranch(ctx, path)
	rules, policyHash, err := localProjectPolicy(path)
	if err != nil {
		return model.Project{}, err
	}
	sum := sha256.Sum256([]byte(repo))
	return model.Project{ID: hex.EncodeToString(sum[:8]), Name: name, Repo: repo, RemoteURL: remoteURL, DefaultBranch: branch, Path: path, Policy: rules, PolicyHash: policyHash}, nil
}

func repositoryIdentity(ctx context.Context, path string) (string, string, error) {
	repo, remoteURL := path, ""
	remoteNames, err := github.Run(ctx, path, "git", "remote")
	if err != nil {
		return "", "", err
	}
	if remotes := strings.Fields(remoteNames); len(remotes) > 0 {
		remote, err := github.Run(ctx, path, "git", "remote", "get-url", remotes[0])
		if err != nil {
			return "", "", fmt.Errorf("repository remote %s: %w", remotes[0], err)
		}
		remoteURL, err = workflow.CanonicalRemote(remote)
		if err != nil {
			return "", "", fmt.Errorf("repository remote: %w", err)
		}
		repo = remoteURL
	}
	return repo, remoteURL, nil
}

func repositoryBranch(ctx context.Context, path string) string {
	branch, _ := github.Run(ctx, path, "git", "branch", "--show-current")
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch, _ = github.Run(ctx, path, "git", "symbolic-ref", "--short", "HEAD")
		return strings.TrimSpace(branch)
	}
	return branch
}

func localProjectPolicy(path string) (model.Policy, string, error) {
	rules := model.Policy{}
	policyHash := ""
	policyYML := filepath.Join(path, ".whirlwind.yml")
	policyYAML := filepath.Join(path, ".whirlwind.yaml")
	if _, err := os.Lstat(policyYAML); err == nil {
		policyYML = policyYAML
	} else if !errors.Is(err, os.ErrNotExist) {
		return rules, policyHash, err
	}
	if _, err := os.Lstat(policyYML); err == nil {
		rules, policyHash, err = policy.LoadForOnboarding(path)
		if err != nil {
			return rules, policyHash, fmt.Errorf("project policy: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return rules, policyHash, err
	}
	return rules, policyHash, nil
}
