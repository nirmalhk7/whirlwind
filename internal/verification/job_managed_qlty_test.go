package verification

import (
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestManagedOfflineQltyRequiresSpecificPolicyCapability(t *testing.T) {
	root := t.TempDir()
	commit := strings.Repeat("a", 40)
	policy := strings.Repeat("b", 64)
	template := JobManifest{
		Version: 1, Purpose: ProjectSetupPurpose, ProjectID: "managed", OperationID: "setup", JobID: "setup",
		ConfigurationHash: policy, PolicyHash: policy, CandidateCommit: commit, BaseCommit: commit, CandidateTree: commit,
		Repository: root, Root: filepath.Join(root, "other"), OfflineDockerImage: model.PinnedOfflineQltyImage,
		Commands: []JobCommand{{Name: "qlty-offline", Command: []string{model.PinnedOfflineQltyCommand}, TimeoutSeconds: 90, ReportFormat: "sarif"}},
	}
	template.BindManagedOfflineQlty()
	if err := template.validateDefinition(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*JobManifest)
	}{
		{"stale scanner configuration", func(m *JobManifest) { m.OfflineQltyConfiguration = policy }},
		{"wrong image", func(m *JobManifest) { m.OfflineDockerImage = "image@sha256:" + policy }},
		{"scoped check must be mandatory", func(m *JobManifest) { v := false; m.Commands[0].Required = &v }},
		{"scanner arguments", func(m *JobManifest) { m.Commands[0].Command = []string{model.PinnedOfflineQltyCommand, "--allow"} }},
		{"host environment", func(m *JobManifest) { m.Commands[0].Environment = map[string]string{"HOME": "/home"} }},
		{"standalone cannot claim scanner authority", func(m *JobManifest) {
			m.ProjectID = ""
			m.OperationID = ""
			m.Purpose = ""
			m.PlanID = "plan"
			m.PlanVersion = 1
			m.PlanHash = policy
			m.PolicyHash = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := template
			m.Commands = append([]JobCommand(nil), template.Commands...)
			test.mutate(&m)
			if err := m.validateDefinition(); err == nil {
				t.Fatal("accepted invalid managed scanner authority")
			}
		})
	}
}
