package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type integrationContextAdapter struct {
	failed                         bool
	revision, content, description string
}

func (a *integrationContextAdapter) Run(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
	calls := []provider.ToolEvidence{{Ref: "mcp://ci", Tool: "list", CallID: "read-success", Success: true}}
	if a.failed {
		calls = append(calls, provider.ToolEvidence{Ref: "mcp://ci", Tool: "details", CallID: "read-failed", Success: false})
	}
	text, _ := json.Marshal(map[string]any{"scanned_integrations": []string{"mcp://ci"}, "sources": []map[string]string{{"connection": "mcp://ci", "id": "build-7", "url": "https://ci.example/build/7", "revision": a.revision, "summary": "Build failure", "content": a.content}}})
	return provider.Output{Text: string(text), Integrations: []model.Integration{{Provider: in.ProviderID, Ref: "mcp://ci", Name: "CI", Kind: "mcp", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "list", Description: a.description, ReadOnly: true}, {Name: "details", ReadOnly: true}}}}, ToolCalls: calls}, nil
}

func integrationDiscoveryFixture(t *testing.T) (*Engine, model.Project, *integrationContextAdapter) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	e, task, _ := testEngine(t)
	ctx := context.Background()
	if err := e.Store.Put(ctx, "workflow_config", "global", model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode}, 0); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(ctx, "providers", "fake")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		t.Fatal(err)
	}
	p.Kind = "codex"
	p.Capabilities = append(p.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", p, record.Version); err != nil {
		t.Fatal(err)
	}
	adapter := &integrationContextAdapter{revision: "rev-1", content: "Regression fails in build 7", description: "List CI builds"}
	e.Adapter = func(model.Provider) provider.Adapter { return adapter }
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return e, project, adapter
}

func TestIntegrationContextRejectsPartialFailedReads(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	adapter.failed = true
	if _, _, err := e.integrationContext(context.Background(), project); err == nil || !strings.Contains(err.Error(), "mcp://ci") {
		t.Fatalf("successful list hid inaccessible source details: %v", err)
	}
}

func TestIntegrationDiscoveryFingerprintIgnoresCaptureMetadata(t *testing.T) {
	ctx := context.Background()
	e, project, adapter := integrationDiscoveryFixture(t)
	_, _, first, _, err := e.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.saveDiscoverySnapshot(ctx, project.ID, first); err != nil {
		t.Fatal(err)
	}
	_, _, second, unchanged, err := e.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged || first.Fingerprint != second.Fingerprint {
		t.Fatal("unchanged native evidence changed discovery identity after retrieval")
	}
	var firstAudit, secondAudit struct {
		Attempt   string `json:"attempt_id"`
		Retrieved string `json:"retrieved_at"`
	}
	if err := json.Unmarshal([]byte(first.Sources), &firstAudit); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second.Sources), &secondAudit); err != nil {
		t.Fatal(err)
	}
	if firstAudit.Attempt == "" || secondAudit.Attempt == "" || firstAudit.Attempt == secondAudit.Attempt || firstAudit.Retrieved == "" || secondAudit.Retrieved == "" {
		t.Fatal("stable identity discarded attempt/retrieval audit evidence")
	}
	for _, change := range []func(){func() { adapter.revision = "rev-2" }, func() { adapter.content = "Different build detail" }, func() { adapter.description = "Different native tool description" }} {
		change()
		_, _, changed, unchanged, err := e.prepareDiscovery(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		if unchanged || changed.Fingerprint == first.Fingerprint {
			t.Fatal("semantic source or inventory change disappeared from fingerprint")
		}
	}
}

func TestIntegrationCandidateFingerprintRetainsStableEvidenceIdentity(t *testing.T) {
	ref := model.EvidenceRef{Kind: "integration", Provider: "codex", Ref: "mcp://ci:build-7", Connection: "mcp://ci", URL: "https://ci.example/build/7", Revision: "rev-1", Summary: "Build failure", AttemptID: "attempt-1", RetrievedAt: "2026-10-22T10:00:00Z"}
	candidate := func() []model.Direction {
		return []model.Direction{{Title: "Fix build failure", Description: "Investigate CI regression", ExpectedBenefit: "Restore builds", EffortEstimate: "Small", ProblemKind: "opportunity", Uncertainty: []string{"Confirm cause"}, Evidence: []model.EvidenceRef{ref}}}
	}
	first := candidate()
	if err := validateCandidateEvidence(first, discoverySnapshot{Evidence: []model.EvidenceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	ref.AttemptID, ref.RetrievedAt = "attempt-2", "2026-10-22T11:00:00Z"
	second := candidate()
	if err := validateCandidateEvidence(second, discoverySnapshot{Evidence: []model.EvidenceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	if first[0].Fingerprint != second[0].Fingerprint {
		t.Fatal("capture metadata allowed duplicate native candidate")
	}
	if second[0].Evidence[0].AttemptID != "attempt-2" || second[0].Evidence[0].RetrievedAt != ref.RetrievedAt {
		t.Fatal("canonical evidence lost audit metadata")
	}
	ref.Revision = "rev-2"
	third := candidate()
	if err := validateCandidateEvidence(third, discoverySnapshot{Evidence: []model.EvidenceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	if third[0].Fingerprint == second[0].Fingerprint {
		t.Fatal("source revision change did not invalidate candidate identity")
	}
}
