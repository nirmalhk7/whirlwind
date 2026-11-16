package verification

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Setup precedes plan authoring. Its evidence must belong to a project and the
// existing setup operation, without pretending that an approved plan exists.
func TestProjectSetupRunsWithoutPlanAndBindsEvidenceToOperation(t *testing.T) {
	binary, base := parityRunnerFixture(t)
	manifest := projectSetupFixture(t, base, nil)
	report, err := RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil {
		t.Fatalf("project setup cannot run before a plan exists: %v", err)
	}
	if !report.Passed || report.PlanID != "" || report.PlanVersion != 0 || report.PlanHash != "" {
		t.Fatalf("setup fabricated plan approval or failed its command: %+v", report)
	}
	encoded, err := json.Marshal(report)
	requireVerificationSuccess(t, err)
	var fields map[string]any
	requireVerificationSuccess(t, json.Unmarshal(encoded, &fields))
	for key, want := range map[string]string{"purpose": "project_setup", "project_id": "project_fixture", "operation_id": "job"} {
		if fields[key] != want {
			t.Fatalf("setup report lost %s binding: %v", key, fields[key])
		}
		assertSetupReportRejectsChangedScope(t, manifest, fields, key)
	}
	assertSetupCannotBecomePlanApproval(t, manifest, report)
}

func assertSetupCannotBecomePlanApproval(t *testing.T, manifest JobManifest, report JobReport) {
	t.Helper()
	manifest.PlanID, manifest.PlanVersion, manifest.PlanHash = "forged-plan", 1, "forged-approval"
	report.PlanID, report.PlanVersion, report.PlanHash = "forged-plan", 1, "forged-approval"
	report.ManifestHash = manifest.Digest()
	for index := range report.Results {
		report.Results[index].ManifestHash = report.ManifestHash
	}
	if err := report.Validate(manifest); err == nil {
		t.Fatal("self-consistent setup report masqueraded as plan approval")
	}
}

func projectSetupFixture(t *testing.T, base JobManifest, change map[string]any) JobManifest {
	t.Helper()
	base.Commands = []JobCommand{{Name: "probe", Command: []string{"/usr/bin/printf", "%s\\n", "scanner-ready"}, TimeoutSeconds: 5}}
	raw, err := json.Marshal(base)
	requireVerificationSuccess(t, err)
	var fields map[string]any
	requireVerificationSuccess(t, json.Unmarshal(raw, &fields))
	delete(fields, "plan_id")
	delete(fields, "plan_version")
	delete(fields, "plan_hash")
	fields["purpose"], fields["project_id"], fields["operation_id"] = "project_setup", "project_fixture", "job"
	for key, value := range change {
		fields[key] = value
	}
	raw, err = json.Marshal(fields)
	requireVerificationSuccess(t, err)
	var manifest JobManifest
	requireVerificationSuccess(t, json.Unmarshal(raw, &manifest))
	return manifest
}

func assertSetupReportRejectsChangedScope(t *testing.T, manifest JobManifest, fields map[string]any, key string) {
	t.Helper()
	before := fields[key]
	fields[key] = "different-scope"
	raw, err := json.Marshal(fields)
	requireVerificationSuccess(t, err)
	fields[key] = before
	var changed JobReport
	requireVerificationSuccess(t, json.Unmarshal(raw, &changed))
	if err := changed.Validate(manifest); err == nil {
		t.Fatalf("setup report accepted changed %s", key)
	}
}

func TestProjectSetupRejectsMissingOrAmbiguousAuthority(t *testing.T) {
	_, base := parityRunnerFixture(t)
	for _, tc := range []struct {
		name   string
		change map[string]any
	}{
		{"missing project", map[string]any{"project_id": ""}},
		{"missing operation", map[string]any{"operation_id": ""}},
		{"different operation", map[string]any{"operation_id": "another-job"}},
		{"missing configuration", map[string]any{"configuration_hash": ""}},
		{"plan identity mixed in", map[string]any{"plan_id": "plan", "plan_version": 1, "plan_hash": "approved"}},
		{"unknown purpose", map[string]any{"purpose": "unrecognized", "plan_id": "plan", "plan_version": 1, "plan_hash": "approved"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := projectSetupFixture(t, base, tc.change)
			if err := manifest.Validate(); err == nil {
				t.Fatal("setup accepted missing or ambiguous authority")
			}
		})
	}
}

func TestProjectSetupReportRejectsMissingOrUnsafeOperationIdentity(t *testing.T) {
	for _, id := range []string{"", "../outside"} {
		manifest := JobManifest{Version: 1, Purpose: ProjectSetupPurpose, ProjectID: "project_fixture", JobID: id, OperationID: id, ConfigurationHash: "configuration"}
		report := JobReport{Purpose: ProjectSetupPurpose, ProjectID: "project_fixture", OperationID: id, ManifestHash: manifest.Digest(), ConfigurationHash: "configuration", Passed: true}
		if err := report.Validate(manifest); err == nil {
			t.Errorf("setup report accepted invalid existing operation identity %q", id)
		}
	}
}

func TestProjectSetupReportCannotBeGreenWithoutCommands(t *testing.T) {
	root := t.TempDir()
	manifest := JobManifest{Version: 1, Purpose: ProjectSetupPurpose, ProjectID: "project_fixture", JobID: "job", OperationID: "job", ConfigurationHash: "configuration", CandidateCommit: strings.Repeat("a", 40), CandidateTree: strings.Repeat("b", 40), BaseCommit: strings.Repeat("c", 40), Repository: filepath.Join(root, "source"), Root: filepath.Join(root, "job"), Launcher: filepath.Join(root, "nono", "launch.mjs")}
	report := JobReport{Purpose: ProjectSetupPurpose, ProjectID: "project_fixture", OperationID: "job", ManifestHash: manifest.Digest(), ConfigurationHash: "configuration", CandidateCommit: manifest.CandidateCommit, CandidateTree: manifest.CandidateTree, Passed: true}
	if err := report.Validate(manifest); err == nil {
		t.Fatal("setup report declared readiness without executing any command")
	}
}
