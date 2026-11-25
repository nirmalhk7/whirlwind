package verification

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type baselineSARIFLocation struct {
	Physical struct {
		Artifact struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region *baselineSARIFRegion `json:"region"`
	} `json:"physicalLocation"`
}

type baselineSARIFRegion struct {
	StartLine   int  `json:"startLine"`
	StartColumn *int `json:"startColumn"`
	EndLine     *int `json:"endLine"`
	EndColumn   *int `json:"endColumn"`
	Snippet     struct {
		Text string `json:"text"`
	} `json:"snippet"`
}

func sarifCoordinate(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func (region *baselineSARIFRegion) coordinateIdentity() (string, error) {
	column := sarifCoordinate(region.StartColumn, 1)
	endLine := sarifCoordinate(region.EndLine, region.StartLine)
	endColumn := sarifCoordinate(region.EndColumn, 0)
	if region.StartLine <= 0 || column <= 0 || endLine < region.StartLine || region.EndColumn != nil && endColumn <= 0 || endLine == region.StartLine && endColumn > 0 && endColumn < column {
		return "", errors.New("baseline comparison blocked: SARIF finding lacks a valid region")
	}
	return fmt.Sprintf("region:%d:%d:%d:%d", region.StartLine, column, endLine, endColumn), nil
}

type baselineSARIFResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text     string `json:"text"`
		Markdown string `json:"markdown"`
	} `json:"message"`
	PartialFingerprints map[string]string       `json:"partialFingerprints"`
	Locations           []baselineSARIFLocation `json:"locations"`
	Properties          map[string]any          `json:"properties"`
}
type baselineSARIFInvocation struct {
	Successful                 *bool                      `json:"executionSuccessful"`
	ExecutionNotifications     baselineSARIFNotifications `json:"toolExecutionNotifications"`
	ConfigurationNotifications baselineSARIFNotifications `json:"toolConfigurationNotifications"`
}
type baselineSARIFRun struct {
	Tool struct {
		Driver struct {
			Name string `json:"name"`
		} `json:"driver"`
	} `json:"tool"`
	Results     json.RawMessage           `json:"results"`
	Invocations []baselineSARIFInvocation `json:"invocations"`
}

func baselineSARIF(output string) ([]baselineFinding, error) {
	var report struct {
		Version string             `json:"version"`
		Runs    []baselineSARIFRun `json:"runs"`
	}
	if json.Unmarshal([]byte(output), &report) != nil || report.Version != "2.1.0" || len(report.Runs) == 0 {
		return nil, errors.New("baseline comparison blocked: Qlty requires complete SARIF 2.1.0 evidence")
	}
	findings := []baselineFinding{}
	for _, run := range report.Runs {
		next, err := run.findings()
		if err != nil {
			return nil, err
		}
		findings = append(findings, next...)
	}
	return findings, nil
}

// ValidateJobSARIF verifies complete SARIF 2.1 evidence, including successful
// invocations and structurally valid findings. It does not require zero findings.
func ValidateJobSARIF(output []byte) error {
	if len(output) == 0 {
		return errors.New("empty Qlty SARIF evidence")
	}
	if _, err := baselineSARIF(string(output)); err != nil {
		return fmt.Errorf("invalid Qlty SARIF evidence: %w", err)
	}
	return nil
}

func (run baselineSARIFRun) findings() ([]baselineFinding, error) {
	if run.Tool.Driver.Name == "" || len(run.Invocations) == 0 {
		return nil, errors.New("SARIF report lacks tool identity or completed invocation evidence")
	}
	for _, invocation := range run.Invocations {
		if err := invocation.validate(); err != nil {
			return nil, err
		}
	}
	if len(run.Results) == 0 || string(run.Results) == "null" {
		return nil, errors.New("baseline comparison blocked: SARIF results missing")
	}
	var results []baselineSARIFResult
	if json.Unmarshal(run.Results, &results) != nil {
		return nil, errors.New("baseline comparison blocked: invalid SARIF results")
	}
	return baselineSARIFFindings(results)
}

func (invocation baselineSARIFInvocation) validate() error {
	if invocation.Successful == nil || !*invocation.Successful {
		return errors.New("baseline comparison blocked: SARIF analysis failed")
	}
	for _, notifications := range []baselineSARIFNotifications{invocation.ExecutionNotifications, invocation.ConfigurationNotifications} {
		for _, notification := range notifications {
			if err := notification.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func baselineSARIFFindings(results []baselineSARIFResult) ([]baselineFinding, error) {
	findings := []baselineFinding{}
	for _, result := range results {
		finding, err := result.finding()
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func (result baselineSARIFResult) finding() (baselineFinding, error) {
	message := result.Message.Text
	if message == "" {
		message = result.Message.Markdown
	}
	if result.RuleID == "" || message == "" {
		return baselineFinding{}, errors.New("baseline comparison blocked: SARIF finding lacks rule or message")
	}
	identity, err := result.identity(message)
	if err != nil {
		return baselineFinding{}, err
	}
	severity := map[string]int{"none": 0, "note": 1, "warning": 2, "": 2, "error": 3}
	rank, ok := severity[result.Level]
	if !ok {
		return baselineFinding{}, errors.New("baseline comparison blocked: unknown SARIF severity")
	}
	metrics := map[string]float64{}
	baselineMetrics("", result.Properties, metrics)
	return baselineFinding{identity, rank, metrics}, nil
}

func (result baselineSARIFResult) identity(message string) (string, error) {
	identity := result.RuleID + ":"
	if len(result.Locations) == 0 {
		return "", errors.New("baseline comparison blocked: SARIF finding lacks stable location")
	}
	if len(result.PartialFingerprints) > 0 {
		identity += result.fingerprint()
		for _, location := range result.Locations {
			if location.Physical.Artifact.URI == "" {
				return "", errors.New("baseline comparison blocked: SARIF finding lacks artifact")
			}
			identity += "|" + location.Physical.Artifact.URI
		}
		return identity, nil
	}
	identity += strings.Join(strings.Fields(message), " ")
	for _, location := range result.Locations {
		suffix, err := location.identitySuffix()
		if err != nil {
			return "", err
		}
		identity += suffix
	}
	return identity, nil
}

func (result baselineSARIFResult) fingerprint() string {
	keys := make([]string, 0, len(result.PartialFingerprints))
	for key := range result.PartialFingerprints {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	identity := ""
	for _, key := range keys {
		identity += key + "=" + result.PartialFingerprints[key] + ";"
	}
	return identity
}

func (location baselineSARIFLocation) identitySuffix() (string, error) {
	if location.Physical.Artifact.URI == "" {
		return "", errors.New("baseline comparison blocked: SARIF finding lacks artifact")
	}
	if location.Physical.Region == nil {
		return "|" + location.Physical.Artifact.URI + "|file", nil
	}
	if location.Physical.Region.Snippet.Text == "" {
		span, err := location.Physical.Region.coordinateIdentity()
		if err != nil {
			return "", err
		}
		return "|" + location.Physical.Artifact.URI + "|" + span, nil
	}
	return "|" + location.Physical.Artifact.URI + "|snippet:" + strings.Join(strings.Fields(location.Physical.Region.Snippet.Text), " "), nil
}

func baselineMetrics(prefix string, values map[string]any, out map[string]float64) {
	for key, value := range values {
		name := prefix + key
		switch v := value.(type) {
		case float64:
			out[name] = v
		case map[string]any:
			baselineMetrics(name+".", v, out)
		}
	}
}

func baselineQlty(r Result) bool {
	return len(r.Command) > 1 && filepath.Base(r.Command[0]) == "qlty" && (r.Command[1] == "check" || r.Command[1] == "smells")
}

func baselineProcessEvidence(r Result) error {
	if baselineQlty(r) {
		if r.QualityIdentity == nil {
			return errors.New("baseline comparison blocked: Qlty runtime identity missing")
		}
		if err := r.QualityIdentity.Validate(); err != nil {
			return fmt.Errorf("baseline comparison blocked: Qlty runtime identity invalid: %w", err)
		}
	}
	if r.Truncated {
		return errors.New("baseline comparison blocked: truncated report")
	}
	if r.ExitCode == nil || *r.ExitCode < 0 || *r.ExitCode > 1 {
		return errors.New("baseline comparison blocked: missing exit status or tool crash")
	}
	if r.Error != "" && r.Error != "exit status 1" && !baselineAdvisoryProcessError(r) {
		return errors.New("baseline comparison blocked: infrastructure error: " + r.Error)
	}
	if r.Passed && *r.ExitCode != 0 {
		return errors.New("baseline comparison blocked: inconsistent process evidence")
	}
	return nil
}

func baselineAdvisoryProcessError(r Result) bool {
	if len(r.Command) < 2 || filepath.Base(r.Command[0]) != "qlty" || r.Command[1] != "smells" {
		return false
	}
	findings, err := baselineSARIF(r.Output)
	if err != nil || len(findings) == 0 {
		return false
	}
	want := fmt.Sprintf("qlty reported %d code smells; repair findings before review", len(findings))
	return r.Error == want
}
