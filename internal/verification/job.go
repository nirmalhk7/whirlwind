package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

// JobManifest is trusted input from the local supervisor or an offline check
// operator. It has no store handle or authority to change workflow state.
type JobManifest struct {
	Purpose            string                 `json:"purpose,omitempty"`
	ProjectID          string                 `json:"project_id,omitempty"`
	OperationID        string                 `json:"operation_id,omitempty"`
	Quality            *model.QualitySnapshot `json:"quality,omitempty"`
	Version            int                    `json:"version"`
	JobID              string                 `json:"job_id"`
	PlanID             string                 `json:"plan_id"`
	PlanVersion        int                    `json:"plan_version"`
	PlanHash           string                 `json:"plan_hash"`
	ConfigurationHash  string                 `json:"configuration_hash"`
	CandidateCommit    string                 `json:"candidate_commit"`
	CandidateTree      string                 `json:"candidate_tree"`
	BaseCommit         string                 `json:"base_commit"`
	Repository         string                 `json:"repository"`
	Root               string                 `json:"root"`
	Launcher           string                 `json:"launcher"`
	OfflineDockerImage string                 `json:"offline_docker_image,omitempty"`
	Sandbox            *model.SandboxPolicy   `json:"sandbox,omitempty"`
	Commands           []JobCommand           `json:"commands"`
}

type JobCommand struct {
	Mode           string            `json:"mode,omitempty"`
	Name           string            `json:"name"`
	Command        []string          `json:"command"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	DependsOn      []string          `json:"depends_on,omitempty"`
	Required       *bool             `json:"required,omitempty"`
	OutputPaths    []string          `json:"output_paths,omitempty"`
	ReportFormat   string            `json:"report_format,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
}

type JobResult struct {
	Result
	Status            string `json:"status"`
	StdoutPath        string `json:"stdout_path"`
	StderrPath        string `json:"stderr_path"`
	StdoutSHA256      string `json:"stdout_sha256,omitempty"`
	StderrSHA256      string `json:"stderr_sha256,omitempty"`
	ManifestHash      string `json:"manifest_hash"`
	CandidateCommit   string `json:"candidate_commit"`
	CandidateTree     string `json:"candidate_tree"`
	ConfigurationHash string `json:"configuration_hash"`
}

type JobReport struct {
	QualityIdentity   *model.QualityIdentity `json:"quality_identity,omitempty"`
	Purpose           string                 `json:"purpose,omitempty"`
	ProjectID         string                 `json:"project_id,omitempty"`
	OperationID       string                 `json:"operation_id,omitempty"`
	ManifestHash      string                 `json:"manifest_hash"`
	PlanID            string                 `json:"plan_id"`
	PlanVersion       int                    `json:"plan_version"`
	PlanHash          string                 `json:"plan_hash"`
	ConfigurationHash string                 `json:"configuration_hash"`
	CandidateCommit   string                 `json:"candidate_commit"`
	CandidateTree     string                 `json:"candidate_tree"`
	Passed            bool                   `json:"passed"`
	Results           []JobResult            `json:"results"`
}

type JobEvent struct {
	Type         string     `json:"type"`
	ManifestHash string     `json:"manifest_hash"`
	Index        int        `json:"index"`
	Result       *JobResult `json:"result,omitempty"`
}

var ErrJobSetup = errors.New("local verification setup or recovery requires attention")

var jobIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var gitObject = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

func (m JobManifest) Digest() string {
	raw, _ := json.Marshal(m)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (m JobManifest) Validate() error {
	if m.OfflineDockerImage != "" {
		if runtime.GOOS != "linux" {
			return errors.New("offline Docker verification requires Linux")
		}
	} else if runtime.GOOS != "darwin" {
		return errors.New("local verification execution currently requires macOS; inspection remains available")
	}
	return m.validateDefinition()
}

// Report readers validate the same frozen definition without requiring the
// inspecting host to support native execution.
func (m JobManifest) validateDefinition() error {
	if m.Version != 1 || !jobIdentifier.MatchString(m.JobID) {
		return errors.New("verification manifest requires supported version and job identity")
	}
	if err := m.validateAuthority(); err != nil {
		return err
	}
	if !gitObject.MatchString(m.CandidateCommit) || !gitObject.MatchString(m.CandidateTree) || !gitObject.MatchString(m.BaseCommit) {
		return errors.New("verification requires frozen Git object identities")
	}
	if !filepath.IsAbs(m.Repository) || !filepath.IsAbs(m.Root) || (m.OfflineDockerImage == "" && !filepath.IsAbs(m.Launcher)) {
		return errors.New("verification requires absolute repository, staging, and launcher paths")
	}
	if m.Quality != nil {
		if err := m.Quality.Validate(); err != nil {
			return err
		}
		if m.Purpose != ProjectSetupPurpose && m.Quality.Identity == nil && hasQltyCheck(m.Commands) {
			return errors.New("Qlty verification requires an approved runtime identity; rerun project setup")
		}
		if m.Purpose != ProjectSetupPurpose && m.Quality.Identity == nil && hasQltyCheck(m.Commands) {
			return errors.New("Qlty verification requires an approved runtime identity; rerun project setup")
		}
		if filepath.Clean(m.Root) != filepath.Join(m.Quality.Root, "jobs", m.JobID) {
			return errors.New("quality staging must stay under approved external quality root")
		}
	}
	if len(m.Commands) == 0 || len(m.Commands) > 128 {
		return errors.New("verification requires 1 to 128 approved commands")
	}
	if err := validateJobCommands(m.Commands); err != nil {
		return err
	}
	if m.OfflineDockerImage != "" {
		return m.validateOfflineDocker()
	}
	if hasQltyCheck(m.Commands) && m.Quality == nil {
		return errors.New("Qlty verification requires frozen external quality configuration")
	}
	return nil
}

func hasQltyCheck(commands []JobCommand) bool {
	for _, command := range commands {
		if len(command.Command) > 1 && filepath.Base(command.Command[0]) == "qlty" && (command.Command[1] == "check" || command.Command[1] == "smells") {
			return true
		}
	}
	return false
}

func RunJob(ctx context.Context, m JobManifest, observe func(JobEvent) error) (JobReport, error) {
	if err := m.Validate(); err != nil {
		return JobReport{}, err
	}
	runner, err := openJob(m)
	if err != nil {
		return JobReport{}, err
	}
	defer runner.close()
	if err := process.Reap(filepath.Join(m.Root, "processes")); err != nil {
		return JobReport{}, fmt.Errorf("verification orphan cleanup failed: %w", err)
	}
	return runner.run(ctx, observe)
}

func writeJobJSON(root *os.Root, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(path+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		if err := root.Remove(path + ".tmp"); err != nil {
			return err
		}
		file, err = root.OpenFile(path+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if err != nil {
		return err
	}
	_, err = file.Write(append(raw, '\n'))
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return root.Rename(path+".tmp", path)
}

func validateJobCommands(commands []JobCommand) error {
	seen := map[string]bool{}
	for _, command := range commands {
		if err := validateJobCommand(command, seen); err != nil {
			return err
		}
		seen[command.Name] = true
	}
	return nil
}

func validateJobCommand(command JobCommand, seen map[string]bool) error {
	if !slices.Contains([]string{"", "verify", "autofix"}, command.Mode) {
		return fmt.Errorf("unsupported approved command mode %q", command.Mode)
	}
	if !jobIdentifier.MatchString(command.Name) || seen[command.Name] || len(command.Command) == 0 || command.Command[0] == "" || command.TimeoutSeconds < 1 {
		return fmt.Errorf("invalid approved command %q", command.Name)
	}
	if err := validateJobArguments(command.Command); err != nil {
		return err
	}
	if err := validateJobDependencies(command, seen); err != nil {
		return err
	}
	if err := validateJobEnvironment(command); err != nil {
		return err
	}
	if err := validateJobOutputPaths(command.OutputPaths); err != nil {
		return err
	}
	if !slices.Contains([]string{"", "text", "go-test-json", "sarif"}, command.ReportFormat) {
		return fmt.Errorf("unsupported command report format %q", command.ReportFormat)
	}
	return nil
}

func validateJobArguments(arguments []string) error {
	for _, arg := range arguments {
		if strings.ContainsRune(arg, 0) {
			return errors.New("command arguments cannot contain NUL")
		}
	}
	return nil
}

func validateJobDependencies(command JobCommand, seen map[string]bool) error {
	for _, dependency := range command.DependsOn {
		if !seen[dependency] {
			return fmt.Errorf("command %s dependency %s must precede it", command.Name, dependency)
		}
	}
	return nil
}

func validateJobEnvironment(command JobCommand) error {
	for key, value := range command.Environment {
		if !execution.EnvironmentOverrideAllowed(key, &model.SandboxPolicy{Environment: []string{key}}) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid environment entry for %s", command.Name)
		}
	}
	return nil
}

func validateJobOutputPaths(outputs []string) error {
	for _, output := range outputs {
		clean := filepath.Clean(output)
		if !filepath.IsLocal(clean) || clean == "." || clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.HasPrefix(clean, ".whirlwind.") {
			return fmt.Errorf("unsafe command output directory %q", output)
		}
	}
	return nil
}
