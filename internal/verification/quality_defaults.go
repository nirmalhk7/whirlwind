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
// The captured catalog supplies the scanner version. Qualification verifies execution.
// The resulting configuration still needs qualification and human approval.
func (m JobManifest) PrepareQualityDefaults(catalog []byte) (model.QualitySnapshot, error) {
	if err := m.validateDefinition(); err != nil {
		return model.QualitySnapshot{}, err
	}
	if m.Purpose != ProjectSetupPurpose || m.Quality == nil {
		return model.QualitySnapshot{}, errors.New("only project setup may prepare external defaults")
	}
	files, err := defaultQualityFiles(catalog)
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

func defaultQualityFiles(catalog []byte) (map[string]string, error) {
	version, err := defaultGitleaksVersion(catalog)
	if err != nil {
		return nil, err
	}
	configuration := fmt.Sprintf(`config_version = "0"
[[source]]
name = "default"
default = true
[[plugin]]
name = "gitleaks"
version = %s
`, tomlQualityString(version))
	return map[string]string{
		".qlty/qlty.toml": configuration,
		".gitleaks.toml":  "[extend]\nuseDefault = true\n",
	}, nil
}

func tomlQualityString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func defaultGitleaksVersion(raw []byte) (string, error) {
	if len(raw) > 4<<20 {
		return "", errors.New("qlty catalog exceeds retained limit")
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
		return "", fmt.Errorf("read Qlty scanner catalog: %w", err)
	}
	definition := catalog.Plugins.Definitions["gitleaks"]
	if _, hasLint := definition.Drivers["lint"]; strings.TrimSpace(definition.Version) == "" || !hasLint {
		return "", errors.New("qlty catalog lacks version or lint capability for gitleaks")
	}
	return definition.Version, nil
}
