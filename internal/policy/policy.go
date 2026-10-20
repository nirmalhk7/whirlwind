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

	"whirlwind/internal/model"
)

// Validate rejects missing baseline checks, malformed risk rules, negative run budgets, and allowed paths outside the repository.
func Validate(p model.Policy) error {
	if p.RunTimeoutSeconds < 0 {
		return errors.New("run_timeout_seconds must be nonnegative")
	}
	if len(p.Checks) == 0 || len(p.AllowedPaths) == 0 || len(p.RiskRules) == 0 {
		return errors.New("policy requires checks, allowed_paths and risk_rules")
	}
	if err := validateChecks(p.Checks); err != nil {
		return err
	}
	if err := validateRiskRules(p.RiskRules); err != nil {
		return err
	}
	return validateAllowedPaths(p.AllowedPaths)
}

const (
	policyYAML = ".whirlwind.yaml"
	policyYML  = ".whirlwind.yml"
)

// Load reads committed project policy, requires the local file to match it, validates known fields, and returns its content hash.
func Load(path string) (model.Policy, string, error) {
	var p model.Policy
	b, e := committedPolicy(path)
	if e != nil {
		return p, "", e
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

func committedPolicy(path string) ([]byte, error) {
	name, e := policyName(path)
	if e != nil {
		return nil, e
	}
	cmd := exec.Command("git", "-C", path, "show", "HEAD:"+name)
	b, e := cmd.Output()
	if e != nil {
		return nil, errors.New("committed .whirlwind.yml or .whirlwind.yaml required; review and commit project policy, then register again")
	}
	local, e := os.ReadFile(filepath.Join(path, name))
	if e != nil || string(local) != string(b) {
		return nil, errors.New(name + " must match committed policy")
	}
	return b, nil
}

func policyName(path string) (string, error) {
	name := policyYAML
	if _, err := os.Lstat(filepath.Join(path, policyYML)); err == nil {
		name = policyYML
		if _, err := os.Lstat(filepath.Join(path, policyYAML)); err == nil {
			return "", errors.New("keep only one project policy: .whirlwind.yml or .whirlwind.yaml")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return name, nil
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
	if unsafePath(path) {
		return false
	}
	for _, a := range p.AllowedPaths {
		if Match(a, path) {
			return true
		}
	}
	return false
}

func validateChecks(checks []model.Check) error {
	names := map[string]bool{}
	for _, c := range checks {
		if c.Name == "" || len(c.Command) == 0 || names[c.Name] {
			return errors.New("checks need unique names and command arrays")
		}
		names[c.Name] = true
	}
	return nil
}

func validateRiskRules(rules []model.RiskRule) error {
	for _, rule := range rules {
		if err := validateRiskRule(rule); err != nil {
			return err
		}
	}
	return nil
}
func validateRiskRule(r model.RiskRule) error {
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
	return nil
}

func unsafePath(path string) bool {
	switch path {
	case policyYAML, policyYML, ".git", "..":
		return true
	}
	return !filepath.IsLocal(path) || strings.HasPrefix(path, ".git/")
}

func validateAllowedPaths(paths []string) error {
	for _, a := range paths {
		if !filepath.IsLocal(a) {
			return errors.New("allowed paths must stay inside repository")
		}
	}
	return nil
}
