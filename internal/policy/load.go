package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"whirlwind/internal/model"
)

const (
	policyYAML = ".whirlwind.yaml"
	policyYML  = ".whirlwind.yml"
)

// Load reads committed project policy, requires the local file to match it, validates known fields, and returns its content hash.
func Load(path string) (model.Policy, string, error) {
	var p model.Policy
	b, err := committedPolicy(path)
	if err != nil {
		return p, "", err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(b)))
	decoder.KnownFields(true)
	if err = decoder.Decode(&p); err != nil {
		return p, "", err
	}
	if err = Validate(p); err != nil {
		return p, "", err
	}
	sum := sha256.Sum256(b)
	return p, hex.EncodeToString(sum[:]), nil
}

func committedPolicy(path string) ([]byte, error) {
	name, err := policyName(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "-C", path, "show", "HEAD:"+name)
	b, err := cmd.Output()
	if err != nil {
		return nil, errors.New("committed .whirlwind.yml or .whirlwind.yaml required; review and commit project policy, then register again")
	}
	local, err := os.ReadFile(filepath.Join(path, name))
	if err != nil || string(local) != string(b) {
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
