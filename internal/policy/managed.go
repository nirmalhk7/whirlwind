package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/model"
)

const ManagedPolicyResource = "project_policy_documents"
const MaxManagedPolicyBytes = 64 << 10

// ParseManagedPolicy validates the exact text shown to the operator. Its
// approval fingerprint is scoped to the registered project.
func ParseManagedPolicy(projectID, content string) (model.Policy, string, error) {
	var rules model.Policy
	if len(content) > MaxManagedPolicyBytes {
		return rules, "", errors.New("managed policy exceeds 64 KiB")
	}
	if projectID == "" || !utf8.ValidString(content) {
		return rules, "", errors.New("managed policy requires a project and UTF-8 YAML")
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rules); err != nil {
		return rules, "", err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return rules, "", errors.New("managed policy must contain exactly one YAML document")
	}
	if err := Validate(rules); err != nil {
		return rules, "", err
	}
	if rules.OfflineDockerImage != "" {
		if err := validateManagedOfflinePolicy(rules); err != nil {
			return rules, "", err
		}
	}
	hash := sha256.Sum256([]byte("whirlwind:project-policy:v1\x00" + projectID + "\x00" + content))
	return rules, hex.EncodeToString(hash[:]), nil
}

func validateManagedOfflinePolicy(rules model.Policy) error {
	if !model.IsOfflineDockerImage(rules.OfflineDockerImage) || rules.Quality != nil || rules.Sandbox != nil {
		return errors.New("offline Docker requires a digest-pinned image and an external check policy without native scanner or sandbox settings")
	}
	checks := append([]model.Check(nil), rules.Checks...)
	for _, risk := range rules.RiskRules {
		checks = append(checks, risk.Checks...)
	}
	for _, check := range checks {
		if model.IsPinnedOfflineQltyCheck(check) && rules.OfflineDockerImage != model.PinnedOfflineQltyImage {
			return errors.New("pinned offline Qlty requires the scanner-qualified image")
		}
		if check.Reusable {
			return errors.New("managed offline checks require fresh execution evidence")
		}
		if err := model.ValidateOfflineDockerCheck(check); err != nil {
			return err
		}
	}
	return nil
}

func CheckManagedPolicyLocation(state, checkout string) error {
	root, err := filepath.EvalSymlinks(state)
	if err != nil {
		return err
	}
	repository, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(repository, root)
	if err != nil || filepath.IsLocal(relative) {
		return errors.New("supervisor state must be outside the project checkout")
	}
	return nil
}
