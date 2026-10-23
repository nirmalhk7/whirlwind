package verification

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

type baselineSARIFLocation struct {
	Physical struct {
		Artifact struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			Snippet struct {
				Text string `json:"text"`
			} `json:"snippet"`
		} `json:"region"`
	} `json:"physicalLocation"`
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
type baselineSARIFNotification struct {
	Level string `json:"level"`
}
type baselineSARIFInvocation struct {
	Successful                 *bool                       `json:"executionSuccessful"`
	ExecutionNotifications     []baselineSARIFNotification `json:"toolExecutionNotifications"`
	ConfigurationNotifications []baselineSARIFNotification `json:"toolConfigurationNotifications"`
}
type baselineSARIFRun struct {
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

func (run baselineSARIFRun) findings() ([]baselineFinding, error) {
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
	if invocation.Successful != nil && !*invocation.Successful {
		return errors.New("baseline comparison blocked: SARIF analysis failed")
	}
	for _, notification := range append(invocation.ExecutionNotifications, invocation.ConfigurationNotifications...) {
		if notification.Level == "error" {
			return errors.New("baseline comparison blocked: SARIF tool error")
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
	if len(result.PartialFingerprints) > 0 {
		return identity + result.fingerprint(), nil
	}
	if len(result.Locations) == 0 {
		return "", errors.New("baseline comparison blocked: SARIF finding lacks stable location or fingerprint")
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
	if location.Physical.Artifact.URI == "" || location.Physical.Region.Snippet.Text == "" {
		return "", errors.New("baseline comparison blocked: SARIF finding lacks artifact or region snippet")
	}
	return "|" + location.Physical.Artifact.URI + "|" + strings.Join(strings.Fields(location.Physical.Region.Snippet.Text), " "), nil
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
	return baselineQlty(r) && strings.HasPrefix(r.Error, "Qlty reported ") && strings.Contains(r.Error, "code smells")
}
