package verification

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/model"
)

// PrepareQualityDefaults publishes only missing external configuration files.
// Captured catalog/rule bytes are inputs, not proof that scanners executed.
// The resulting configuration still needs qualification and human approval.
func (m JobManifest) PrepareQualityDefaults(catalog, registry []byte) (model.QualitySnapshot, error) {
	if err := m.validateDefinition(); err != nil {
		return model.QualitySnapshot{}, err
	}
	if m.Purpose != ProjectSetupPurpose || m.Quality == nil {
		return model.QualitySnapshot{}, errors.New("only project setup may prepare external defaults")
	}
	files, err := defaultQualityFiles(catalog, registry)
	if err != nil {
		return model.QualitySnapshot{}, err
	}
	snapshot := model.QualitySnapshot{Root: m.Quality.Root, Files: files}
	if err := snapshot.Validate(); err != nil {
		return snapshot, err
	}
	root, err := openDefaultQualityRoot(m.Repository, snapshot.Root)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = root.Close() }()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := checkQualityTarget(root, "config/"+path, false); err != nil {
			return snapshot, err
		}
	}
	for _, path := range paths {
		content, err := publishDefaultQualityFile(root, "config/"+path, files[path])
		if err != nil {
			return snapshot, err
		}
		snapshot.Files[path] = content
	}
	return snapshot, snapshot.Validate()
}

func defaultQualityFiles(catalog, registry []byte) (map[string]string, error) {
	versions, err := defaultScannerVersions(catalog)
	if err != nil {
		return nil, err
	}
	rules, err := frozenSemgrepRules(registry)
	if err != nil {
		return nil, err
	}
	configuration := fmt.Sprintf(`config_version = "0"
[[source]]
name = "default"
default = true
[[plugin]]
name = "semgrep"
version = %s
[[plugin]]
name = "gitleaks"
version = %s
[[plugins.definitions.semgrep.environment]]
name = "SSL_CERT_FILE"
value = "${env.SSL_CERT_FILE}"
[plugins.definitions.semgrep.drivers.lint]
script = "semgrep --metrics=off --disable-version-check --config .semgrep/ci.yaml --sarif --output=${tmpfile} ${target}"
`, tomlQualityString(versions["semgrep"]), tomlQualityString(versions["gitleaks"]))
	return map[string]string{
		".qlty/qlty.toml":  configuration,
		".semgrep/ci.yaml": rules,
		".gitleaks.toml":   "[extend]\nuseDefault = true\n",
	}, nil
}

func tomlQualityString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func defaultScannerVersions(raw []byte) (map[string]string, error) {
	if len(raw) > 4<<20 {
		return nil, errors.New("qlty catalog exceeds retained limit")
	}
	var catalog struct {
		Plugins struct {
			Definitions map[string]struct {
				Version string                    `yaml:"known_good_version"`
				Drivers map[string]map[string]any `yaml:"drivers"`
			} `yaml:"definitions"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("read Qlty scanner catalog: %w", err)
	}
	versions := map[string]string{}
	for _, name := range []string{"semgrep", "gitleaks"} {
		definition := catalog.Plugins.Definitions[name]
		_, hasLint := definition.Drivers["lint"]
		if strings.TrimSpace(definition.Version) == "" || !hasLint {
			return nil, fmt.Errorf("qlty catalog lacks version or lint capability for %s", name)
		}
		versions[name] = definition.Version
	}
	return versions, nil
}

func frozenSemgrepRules(raw []byte) (string, error) {
	if len(raw) > 4<<20 {
		return "", errors.New("semgrep rule response exceeds retained limit")
	}
	var response map[string]json.RawMessage
	content := raw
	if err := json.Unmarshal(raw, &response); err == nil {
		if wrapped, ok := response["rule_config"]; ok {
			var text string
			if err := json.Unmarshal(wrapped, &text); err == nil {
				content = []byte(text)
			} else {
				content = wrapped
			}
		}
	}
	var configuration struct {
		Rules []struct {
			ID string `yaml:"id"`
		} `yaml:"rules"`
	}
	if len(content) == 0 || len(content) > 1<<20 {
		return "", errors.New("frozen Semgrep rules exceed configuration limit")
	}
	if err := yaml.Unmarshal(content, &configuration); err != nil {
		return "", fmt.Errorf("read frozen Semgrep rules: %w", err)
	}
	if len(configuration.Rules) == 0 {
		return "", errors.New("frozen Semgrep rule set is empty")
	}
	seen := map[string]bool{}
	for _, rule := range configuration.Rules {
		if strings.TrimSpace(rule.ID) == "" || seen[rule.ID] {
			return "", errors.New("frozen Semgrep rules require unique nonempty IDs")
		}
		seen[rule.ID] = true
	}
	return string(content), nil
}
