package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// path resolves symlinks and nonexistent path suffixes while rejecting worktree escapes and direct Git metadata access.
func (c *Client) path(requested string) (string, error) {
	root, err := filepath.EvalSymlinks(c.input.CWD)
	if err != nil {
		return "", err
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = resolvePathSuffix(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("ACP path escapes worktree")
	}
	if relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", errors.New("ACP direct Git metadata access denied")
	}
	return candidate, nil
}

// resolvePathSuffix resolves the existing prefix and retains missing descendant names.
func resolvePathSuffix(candidate string) (string, error) {
	existing := candidate
	suffix := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
}

// allow rejects mutations for read-only roles and asks the configured hook to authorize a tool request.
func (c *Client) allow(ctx context.Context, p Permission) error {
	if c.input.ReadOnly {
		return errors.New("read-only agent role")
	}
	if c.hooks.Permission == nil {
		return errors.New("permission handler unavailable")
	}
	ok, err := c.hooks.Permission(ctx, p)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("tool permission denied")
	}
	return nil
}
