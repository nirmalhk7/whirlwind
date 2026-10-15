package policy

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"whirlwind/internal/model"
)

// LoadForOnboarding accepts an empty draft for ideation only. Execution still uses Load.
func LoadForOnboarding(path string) (model.Policy, string, error) {
	p, hash, loadErr := Load(path)
	if loadErr == nil {
		return p, hash, nil
	}
	return model.Policy{}, "", validateEmptyDraft(path, loadErr)
}

func validateEmptyDraft(path string, loadErr error) error {
	name, err := policyName(path)
	if err != nil {
		return err
	}
	local, err := readEmptyDraft(filepath.Join(path, name), loadErr)
	if err != nil {
		return err
	}
	// An empty local file cannot replace a different committed execution policy.
	committed, err := exec.Command("git", "-C", path, "show", "HEAD:"+name).Output()
	if err == nil && string(committed) != string(local) {
		return loadErr
	}
	return nil
}

func readEmptyDraft(path string, loadErr error) ([]byte, error) {
	local, err := os.ReadFile(path)
	if err != nil {
		return nil, loadErr
	}
	var draft model.Policy
	decoder := yaml.NewDecoder(strings.NewReader(string(local)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&draft); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if !emptyDraft(draft) {
		return nil, loadErr
	}
	return local, nil
}

func emptyDraft(draft model.Policy) bool {
	selectors := len(draft.Checks)+len(draft.AllowedPaths)+len(draft.RiskRules)+len(draft.Commands)+len(draft.Providers) == 0
	budgets := draft.RunTimeoutSeconds == 0 && draft.GlobalSlots == 0 && draft.ProjectSlots == 0
	return selectors && budgets && draft.Embeddings == nil
}
