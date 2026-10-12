package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"khan/internal/model"
)

// Validate rejects missing baseline checks, malformed risk rules, negative run budgets, and allowed paths outside the repository.
func Validate(p model.Policy) error {
	if p.RunTimeoutSeconds < 0 {
		return errors.New("run_timeout_seconds must be nonnegative")
	}
	if len(p.Checks) == 0 || len(p.AllowedPaths) == 0 || len(p.RiskRules) == 0 {
		return errors.New("policy requires checks, allowed_paths and risk_rules")
	}
	names := map[string]bool{}
	for _, c := range p.Checks {
		if c.Name == "" || len(c.Command) == 0 || names[c.Name] {
			return errors.New("checks need unique names and command arrays")
		}
		names[c.Name] = true
	}
	for _, r := range p.RiskRules {
		switch r.Level {
		case "low", "medium", "high", "critical":
		default:
			return errors.New("invalid risk level")
		}
		if len(r.Paths) == 0 && len(r.APISymbols) == 0 && len(r.CodeTypes) == 0 {
			return errors.New("risk selector required")
		}
		for _, c := range r.Checks {
			if c.Name == "" || len(c.Command) == 0 {
				return errors.New("risk check command required")
			}
		}
	}
	for _, a := range p.AllowedPaths {
		if filepath.IsAbs(a) || strings.Contains(a, "..") {
			return errors.New("allowed paths must stay inside repository")
		}
	}
	return nil
}

// Load reads committed .khan.yaml, requires the local file to match it, validates known fields, and returns its content hash.
func Load(path string) (model.Policy, string, error) {
	var p model.Policy
	cmd := exec.Command("git", "-C", path, "show", "HEAD:.khan.yaml")
	b, e := cmd.Output()
	if e != nil {
		return p, "", errors.New("committed .khan.yaml required")
	}
	local, e := os.ReadFile(filepath.Join(path, ".khan.yaml"))
	if e != nil || string(local) != string(b) {
		return p, "", errors.New(".khan.yaml must match committed policy")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(b)))
	decoder.KnownFields(true)
	if e = decoder.Decode(&p); e != nil {
		return p, "", e
	}
	if e = Validate(p); e != nil {
		return p, "", e
	}
	sum := sha256.Sum256(b)
	return p, hex.EncodeToString(sum[:]), nil
}

// AllowedCommand allows only commands whose argument arrays begin with an explicitly configured prefix.
func AllowedCommand(p model.Policy, args []string) bool {
	for _, prefix := range p.Commands {
		if len(prefix) == 0 || len(args) < len(prefix) {
			continue
		}
		if slices.Equal(args[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// Match matches normalized paths using ordinary globs, repository-wide **, or a recursive directory suffix.
func Match(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	if pattern == "**" {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		return path == base || strings.HasPrefix(path, base+"/")
	}
	ok, _ := filepath.Match(pattern, path)
	return ok
}

// AllowedPath checks configured path patterns while always denying Git metadata, policy edits, and paths outside the repository.
func AllowedPath(p model.Policy, path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == ".khan.yaml" || strings.HasPrefix(path, ".git/") || path == ".git" || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return false
	}
	for _, a := range p.AllowedPaths {
		if Match(a, path) {
			return true
		}
	}
	return false
}
