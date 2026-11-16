package model

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
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
	Root          string            `json:"root"`
	Files         map[string]string `json:"files"`
	PluginCatalog []byte            `json:"plugin_catalog,omitempty"`
	Identity      *QualityIdentity  `json:"identity,omitempty"`
}

// QualityIdentity binds a Qlty report to the actual Qlty executable, installed
// scanner package tree, and exact external configuration/rule bytes.
type QualityIdentity struct {
	Version           int                   `json:"version"`
	QltyVersion       string                `json:"qlty_version"`
	QltyBinarySHA256  string                `json:"qlty_binary_sha256"`
	ConfigurationHash string                `json:"configuration_sha256"`
	ToolRuntimeHash   string                `json:"tool_runtime_sha256"`
	PluginCatalogHash string                `json:"plugin_catalog_sha256"`
	ToolRuntimePath   string                `json:"tool_runtime_path"`
	Tools             []QualityToolIdentity `json:"tools"`
}

type QualityToolIdentity struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

var qualitySHA256 = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (identity QualityIdentity) Validate() error {
	if identity.Version != 1 || strings.TrimSpace(identity.QltyVersion) == "" || len(identity.QltyVersion) > 256 || strings.ContainsAny(identity.QltyVersion, "\x00\r\n") || !qualitySHA256.MatchString(identity.QltyBinarySHA256) || !qualitySHA256.MatchString(identity.ConfigurationHash) || !qualitySHA256.MatchString(identity.ToolRuntimeHash) || !qualitySHA256.MatchString(identity.PluginCatalogHash) || !filepath.IsAbs(identity.ToolRuntimePath) || filepath.Clean(identity.ToolRuntimePath) != identity.ToolRuntimePath || len(identity.Tools) == 0 || len(identity.Tools) > 64 {
		return errors.New("incomplete frozen quality runtime identity")
	}
	// Decode the hashes too, so later representations cannot loosen the syntax.
	for _, value := range []string{identity.QltyBinarySHA256, identity.ConfigurationHash, identity.ToolRuntimeHash, identity.PluginCatalogHash} {
		if _, err := hex.DecodeString(value); err != nil {
			return errors.New("invalid frozen quality runtime hash")
		}
	}
	previous := ""
	for _, tool := range identity.Tools {
		key := tool.Name + "@" + tool.Version
		if !qualityToolName.MatchString(tool.Name) || key <= previous || strings.TrimSpace(tool.Version) == "" || len(tool.Version) > 128 || strings.ContainsAny(tool.Version, "\x00\r\n") || !qualityFingerprint.MatchString(tool.Fingerprint) {
			return errors.New("invalid frozen scanner runtime identity")
		}
		previous = key
	}
	return nil
}

var qualityToolName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var qualityFingerprint = regexp.MustCompile(`^[a-f0-9]{8,64}$`)

func SameQualityIdentity(a, b *QualityIdentity) bool {
	if a == nil || b == nil || a.Validate() != nil || b.Validate() != nil {
		return false
	}
	if a.Version != b.Version || a.QltyVersion != b.QltyVersion || a.QltyBinarySHA256 != b.QltyBinarySHA256 || a.ConfigurationHash != b.ConfigurationHash || a.ToolRuntimeHash != b.ToolRuntimeHash || a.PluginCatalogHash != b.PluginCatalogHash || a.ToolRuntimePath != b.ToolRuntimePath || len(a.Tools) != len(b.Tools) {
		return false
	}
	return slices.Equal(a.Tools, b.Tools)
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
	if snapshot.Identity != nil {
		if err := snapshot.Identity.Validate(); err != nil {
			return err
		}
		if len(snapshot.PluginCatalog) == 0 && snapshot.Identity.PluginCatalogHash == "" {
			return errors.New("frozen quality identity lacks Qlty plugin catalog evidence")
		}
	}
	if len(snapshot.PluginCatalog) > 4<<20 {
		return errors.New("Qlty plugin catalog exceeds retained limit")
	}
	return nil
}
