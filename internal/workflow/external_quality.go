package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"whirlwind/internal/model"
)

func externalQualityConfigured(project model.Project) bool {
	if project.Policy.Quality != nil {
		return true
	}
	checks := append([]model.Check{}, project.Policy.Checks...)
	for _, rule := range project.Policy.RiskRules {
		checks = append(checks, rule.Checks...)
	}
	for _, check := range checks {
		if len(check.Command) > 0 && filepath.Base(check.Command[0]) == "qlty" {
			return true
		}
	}
	return false
}

func externalQualitySnapshot(project model.Project) (model.QualitySnapshot, error) {
	configuration := project.Policy.Quality
	if configuration == nil {
		configuration = &model.QualityPolicy{}
	}
	root, err := externalQualityRoot(project, configuration)
	if err != nil {
		return model.QualitySnapshot{}, err
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return model.QualitySnapshot{}, err
	}
	defer func() { _ = directory.Close() }()
	paths, err := externalQualityConfigurationPaths(directory, configuration.ConfigurationFiles)
	if err != nil {
		return model.QualitySnapshot{}, err
	}
	snapshot := model.QualitySnapshot{Root: root, Files: map[string]string{}, Identity: project.QualityIdentity}
	var total int
	for _, path := range paths {
		if !model.QualityConfigurationPathAllowed(path) {
			return snapshot, fmt.Errorf("unsafe external quality configuration path %q", path)
		}
		if _, exists := snapshot.Files[path]; exists {
			return snapshot, errors.New("duplicate external quality configuration file")
		}
		content, err := readExternalQualityFile(directory, path, &total)
		if err != nil {
			return snapshot, err
		}
		snapshot.Files[path] = content
	}
	return snapshot, nil
}

func externalQualityConfigurationPaths(directory *os.Root, configured []string) ([]string, error) {
	if len(configured) > 32 {
		return nil, errors.New("too many external quality configuration files")
	}
	if len(configured) > 0 {
		return configured, nil
	}
	paths := []string{".qlty/qlty.toml"}
	for _, path := range []string{".semgrep/ci.yaml", ".gitleaks.toml"} {
		_, err := directory.Lstat(filepath.Join("config", path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func approvedQualitySnapshot(plan model.ExecutionPlan) (*model.QualitySnapshot, error) {
	if plan.Plan == nil || plan.Plan.Approval == nil {
		return nil, nil
	}
	var snapshot *model.QualitySnapshot
	for _, reference := range plan.Plan.Approval.References {
		if reference.Kind != "quality_files" {
			continue
		}
		if snapshot != nil || reference.ID != plan.ProjectID || reference.Hash != evidenceDigest(json.RawMessage(reference.Data)) {
			return nil, errors.New("invalid approved external quality reference")
		}
		var frozen model.QualitySnapshot
		if err := json.Unmarshal(reference.Data, &frozen); err != nil {
			return nil, err
		}
		if err := frozen.Validate(); err != nil {
			return nil, err
		}
		snapshot = &frozen
	}
	return snapshot, nil
}

func externalQualityRootLocation(project model.Project, configuration *model.QualityPolicy) (string, error) {
	root := configuration.Root
	if root == "" {
		if project.ID == "" || strings.ContainsAny(project.ID, "/\\") || project.ID == "." || project.ID == ".." {
			return "", errors.New("invalid project identity for external quality root")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".whirlwind", "projects", project.ID, "qlty")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("external quality root must be absolute")
	}

	return root, nil
}

func externalQualityRoot(project model.Project, configuration *model.QualityPolicy) (string, error) {
	root, err := externalQualityRootLocation(project, configuration)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("external quality setup missing; prepare configuration under %s/config: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("external quality root must be a regular directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	repository, err := filepath.EvalSymlinks(project.Path)
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(repository, root); err != nil || filepath.IsLocal(relative) {
		return "", errors.New("external quality root must be outside source repository")
	}
	return root, nil
}

func externalQualityFilePath(directory *os.Root, path string) (string, error) {
	relative := filepath.Join("config", path)
	prefix := ""
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, component)
		info, err := directory.Lstat(prefix)
		if err != nil {
			return "", fmt.Errorf("external quality configuration %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("external quality configuration cannot use symlinks")
		}
	}
	info, err := directory.Lstat(relative)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("external quality configuration must be regular files")
	}

	return relative, nil
}

func readExternalQualityFile(directory *os.Root, path string, total *int) (string, error) {
	relative, err := externalQualityFilePath(directory, path)
	if err != nil {
		return "", err
	}
	file, err := directory.Open(relative)
	if err != nil {
		return "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	*total += len(content)
	if len(content) == 0 || len(content) > 1<<20 || *total > 4<<20 || !utf8.Valid(content) || strings.ContainsRune(string(content), 0) {
		return "", errors.New("external quality configuration must be bounded nonempty UTF-8 text")
	}
	return string(content), nil
}
