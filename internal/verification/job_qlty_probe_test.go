package verification

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxQltyProbeHasNoWorkflowAuthority(t *testing.T) {
	root := t.TempDir()
	manifest := func() JobManifest {
		return (LinuxQltyProbeRequest{
			JobID: "fixture", Root: filepath.Join(root, "output"), Repository: root,
			CandidateCommit: strings.Repeat("a", 40), CandidateTree: strings.Repeat("b", 40),
		}).Manifest()
	}
	if err := manifest().validateDefinition(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*JobManifest)
	}{
		{"project", func(m *JobManifest) { m.ProjectID = "project" }},
		{"plan", func(m *JobManifest) { m.PlanID = "plan" }},
		{"setup", func(m *JobManifest) { m.Purpose = ProjectSetupPurpose }},
		{"ordinary job", func(m *JobManifest) {
			m.Purpose, m.PlanID, m.PlanVersion, m.PlanHash = "", "plan", 1, "plan-hash"
		}},
		{"changed configuration", func(m *JobManifest) { m.ConfigurationHash = strings.Repeat("a", 64) }},
		{"changed image", func(m *JobManifest) { m.OfflineDockerImage = "example@sha256:" + strings.Repeat("a", 64) }},
		{"extra argv", func(m *JobManifest) { m.Commands[0].Command = append(m.Commands[0].Command, "--fix") }},
		{"inherited environment", func(m *JobManifest) { m.Commands[0].Environment = map[string]string{"HOME": "/host"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest()
			test.mutate(&candidate)
			if err := candidate.validateDefinition(); err == nil {
				t.Fatal("unexpected probe authority")
			}
		})
	}
}

func TestLinuxQltyProbeRequiresSuccessfulPinnedInvocation(t *testing.T) {
	const clean = `{
  "version":"2.1.0",
  "runs":[{
    "tool":{"driver":{"name":"qlty","semanticVersion":"0.668.0"}},
    "invocations":[{
      "executionSuccessful":true,
      "toolExecutionNotifications":[{"descriptor":{"id":"qlty:message:invocation.concluded.success"}}]
    }],
    "results":[]
  }]
}`
	if err := validateLinuxQltyProbeOutput(clean); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, old, replacement string }{
		{"wrong version", "0.668.0", "0.668.1"},
		{"missing results", `"results":[]`, `"other":[]`},
		{"findings", `"results":[]`, `"results":[{"ruleId":"canary"}]`},
		{"unsuccessful", "true", "false"},
		{"failed scanner", probeDriverSuccess, "qlty:message:invocation.concluded.error"},
		{"missing invocation", `"invocations"`, `"untrusted"`},
		{"malformed", `"version"`, "false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLinuxQltyProbeOutput(strings.Replace(clean, test.old, test.replacement, 1)); err == nil {
				t.Fatal("incomplete or failed scanner report accepted")
			}
		})
	}
}
