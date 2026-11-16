package model

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// QualityPolicy is human-owned verification configuration. Files are relative
// to <root>/config and copied into disposable staging after approval.
type QualityPolicy struct {
	Root               string   `json:"root,omitempty" yaml:"root"`
	ConfigurationFiles []string `json:"configuration_files,omitempty" yaml:"configuration_files"`
}

type QualitySnapshot struct {
	Root  string            `json:"root"`
	Files map[string]string `json:"files"`
}

func QualityConfigurationPathAllowed(path string) bool {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return false
	}
	if strings.HasPrefix(path, ".qlty/") {
		return true
	}
	switch path {
	case ".semgrep.yml", ".semgrep.yaml", ".gitleaks.toml", "semgrep.yml", "semgrep.yaml", "gitleaks.toml", "trivy.yaml", "sgconfig.yml":
		return true
	}
	return strings.HasPrefix(path, ".semgrep/") && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"))
}

func (snapshot QualitySnapshot) Validate() error {
	if !filepath.IsAbs(snapshot.Root) || len(snapshot.Files) == 0 || len(snapshot.Files) > 32 {
		return errors.New("invalid frozen external quality configuration")
	}
	total := 0
	for path, content := range snapshot.Files {
		total += len(content)
		if !QualityConfigurationPathAllowed(path) || len(content) == 0 || len(content) > 1<<20 || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			return errors.New("invalid frozen external quality configuration file")
		}
	}
	if total > 4<<20 {
		return errors.New("external quality configuration exceeds retained limit")
	}
	return nil
}
