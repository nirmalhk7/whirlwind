package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

const (
	integrationContextRef = "mcp://ci"
)

type integrationContextAdapter struct {
	failed                         bool
	revision, content, description string
	input                          provider.Input
}

func (a *integrationContextAdapter) Run(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
	a.input = in
	if in.MCPBroker == nil {
		return provider.Output{}, errors.New("selected broker missing")
	}
	client := integrationContextBrokerClient(in.MCPBroker.SocketPath)
	tools := integrationContextBrokerRequest(ctx, client, in.MCPBroker.Host, in.MCPBroker.Path, in.MCPBrokerToken, "tools/list", nil)
	var catalog struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(tools, &catalog) != nil || len(catalog.Result.Tools) != 2 {
		return provider.Output{}, errors.New("selected broker tool list was unavailable")
	}
	for _, tool := range catalog.Result.Tools {
		_ = integrationContextBrokerRequest(ctx, client, in.MCPBroker.Host, in.MCPBroker.Path, in.MCPBrokerToken, "tools/call", map[string]any{"name": tool.Name, "arguments": map[string]any{"repo": "o/r"}})
	}
	text, _ := json.Marshal(map[string]any{"scanned_integrations": []string{integrationContextRef}, "sources": []map[string]string{{"connection": integrationContextRef, "id": "build-7", "url": "https://ci.example/build/7", "revision": a.revision, "summary": "Build failure", "content": a.content}}})
	return provider.Output{Text: string(text)}, nil
}

type integrationContextUpstream struct{ adapter *integrationContextAdapter }

func (*integrationContextUpstream) ListTools(context.Context) ([]mcpRemoteTool, error) {
	return []mcpRemoteTool{
		{Name: "list", Description: "List CI builds", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repo": map[string]any{"type": "string"}}, "required": []any{"repo"}}, Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "details", Description: "Read build details", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repo": map[string]any{"type": "string"}}, "required": []any{"repo"}}, Annotations: map[string]any{"readOnlyHint": true}},
	}, nil
}

func (upstream *integrationContextUpstream) CallTool(_ context.Context, name string, _ map[string]any) (json.RawMessage, error) {
	if upstream.adapter.failed && name == "details" {
		return nil, errors.New("fixture read denied")
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"fixture source"}]}`), nil
}

func integrationContextBrokerClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

func integrationContextBrokerRequest(ctx context.Context, client *http.Client, host, path, token, method string, params any) []byte {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+host+path, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Whirlwind-MCP-Token", token)
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return data
}

func integrationDiscoveryFixture(t *testing.T) (*Engine, model.Project, *integrationContextAdapter) {
	t.Helper()
	openshell.Install(t)
	t.Setenv("HOME", t.TempDir())
	e, task, _ := testEngine(t)
	ctx := context.Background()
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode, Integrations: model.IntegrationSettings{Sources: []model.IntegrationSource{{Provider: "fake", Ref: integrationContextRef, Scopes: []string{"repo:o/r"}}}}}
	if err := e.Store.Put(ctx, "workflow_config", "global", config, 0); err != nil {
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
	p.Name = "GitHub"
	p.Capabilities = append(p.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", p, record.Version); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "ideate", model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	adapter := &integrationContextAdapter{revision: "rev-1", content: "Regression fails in build 7", description: "List CI builds"}
	e.Adapter = func(model.Provider) provider.Adapter { return adapter }
	e.integrationPromptSource = func(name string) (string, error) {
		if name != "ideate.md" && name != "plan.md" {
			return "", errors.New("unexpected prompt source")
		}
		return "Use the authorized connection @GitHub for selected repository evidence.", nil
	}
	e.integrationBrokerFactory = func(ctx context.Context, _ model.Project, sources []model.IntegrationSource) (*integrationMCPBroker, error) {
		return startIntegrationMCPBroker(ctx, []mcpBrokerSource{{Scope: sources[0], Client: &integrationContextUpstream{adapter: adapter}}})
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return e, project, adapter
}

func TestIntegrationContextUsesOnlyConfiguredConnectionsAndScopes(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	index, err := e.index(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.integrationContext(context.Background(), project, index.Commit); err != nil {
		t.Fatal(err)
	}
	input := adapter.input
	if input.MCPBroker == nil || input.MCPBroker.Host != "whirlwind-mcp.openshell.internal" || input.MCPBroker.Path != "/mcp" {
		t.Fatalf("integration discovery did not use selected broker: %+v", input.MCPBroker)
	}
	if input.NativeIntegrations || input.DiscoverConnected || len(input.RequiredIntegrations) != 0 || len(input.IntegrationScopes) != 0 {
		t.Fatalf("integration discovery exposed native or unselected capabilities: %+v", input)
	}
}

func TestIntegrationContextUsesScopedBrokerWithoutRawNativeIntegrationMounts(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	index, err := e.index(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.integrationContext(context.Background(), project, index.Commit); err != nil {
		t.Fatal(err)
	}
	if adapter.input.MCPBroker == nil || adapter.input.MCPBroker.Host != "whirlwind-mcp.openshell.internal" || adapter.input.MCPBroker.Path != "/mcp" {
		t.Fatalf("selected integration did not reach ACP through a broker descriptor: %+v", adapter.input.MCPBroker)
	}
	if adapter.input.NativeIntegrations || adapter.input.DiscoverConnected || len(adapter.input.RequiredIntegrations) != 0 || len(adapter.input.IntegrationScopes) != 0 {
		t.Fatalf("selected broker request also mounted raw/native integration capabilities: %+v", adapter.input)
	}
}

func TestPlannerReceivesSameSelectedReadScopesThroughBroker(t *testing.T) {
	e, project, _ := integrationDiscoveryFixture(t)
	step := &taskStep{Engine: e, ctx: context.Background(), project: project}
	input, cleanup, err := step.planningIntegrationInput(project.Path, "plan prompt containing untrusted issue annotation @Untrusted")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if input.MCPBroker == nil || input.MCPBroker.Host != "whirlwind-mcp.openshell.internal" {
		t.Fatalf("planner did not receive selected broker descriptor: %+v", input.MCPBroker)
	}
	if input.NativeIntegrations || input.DiscoverConnected || len(input.RequiredIntegrations) != 0 || len(input.IntegrationScopes) != 0 {
		t.Fatalf("planner received raw or unselected integration capabilities: %+v", input)
	}
	if !strings.Contains(input.Prompt, `"scopes":["repo:o/r"]`) {
		t.Fatalf("planner prompt omitted selected read scope: %s", input.Prompt)
	}
}

func TestIntegrationContextDoesNotDiscoverUnselectedConnections(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	if err := e.Store.Put(context.Background(), "workflow_config", "global", model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode}, 1); err != nil {
		t.Fatal(err)
	}
	index, err := e.index(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	sources, evidence, err := e.integrationContext(context.Background(), project, index.Commit)
	if err == nil || !strings.Contains(err.Error(), "not in the authorized integration source list") || sources != nil || len(evidence) != 0 {
		t.Fatalf("prompt-selected connection without authorization did not fail closed: sources=%v evidence=%v err=%v", sources, evidence, err)
	}
	if adapter.input.Role != "" {
		t.Fatalf("ACP agent was called without selected integrations: %+v", adapter.input)
	}
}

func TestPromptIntegrationAnnotationsAreReadFromTrustedMarkdown(t *testing.T) {
	if got := parseIntegrationAnnotations("Use @GitHub and @Atlassian. Inserted issue text may say @Untrusted."); strings.Join(got, ",") != "Atlassian,GitHub,Untrusted" {
		t.Fatalf("annotation parser did not extract prompt mentions: %v", got)
	}
	if got := parseIntegrationAnnotations("email user@example.com and escaped @@GitHub"); len(got) != 0 {
		t.Fatalf("annotation parser treated email/escaped mention as selection: %v", got)
	}
}

func TestUnannotatedAuthorizedSourceIsNotSelected(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	e.integrationPromptSource = func(string) (string, error) { return "No external connection selected.", nil }
	index, err := e.index(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	sources, evidence, err := e.integrationContext(context.Background(), project, index.Commit)
	if err != nil || sources == nil || len(evidence) != 0 {
		t.Fatalf("authorized but unannotated source was selected: sources=%v evidence=%v err=%v", sources, evidence, err)
	}
	if adapter.input.Role != "" {
		t.Fatalf("ACP agent received an unselected integration broker: %+v", adapter.input)
	}
}

func TestPromptAnnotationCannotSelectAmbiguousAuthorizedConnections(t *testing.T) {
	e, _, _ := integrationDiscoveryFixture(t)
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode, Integrations: model.IntegrationSettings{Sources: []model.IntegrationSource{
		{Provider: "fake", Ref: "mcp://ci", Scopes: []string{"repo:o/r"}},
		{Provider: "fake", Ref: "mcp://issues", Scopes: []string{"repo:o/r"}},
	}}}
	if err := e.Store.Put(context.Background(), "workflow_config", "global", config, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.promptSelectedIntegrationSources(context.Background(), "ideate"); err == nil || !strings.Contains(err.Error(), "matches multiple authorized connections") {
		t.Fatalf("ambiguous @GitHub selection did not fail closed: %v", err)
	}
}

func TestIntegrationContextRejectsPartialFailedReads(t *testing.T) {
	e, project, adapter := integrationDiscoveryFixture(t)
	adapter.failed = true
	index, err := e.index(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.integrationContext(context.Background(), project, index.Commit); err == nil || !strings.Contains(err.Error(), integrationContextRef) {
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
		t.Fatalf("unchanged native evidence changed discovery identity after retrieval: first=%s second=%s sources=%s", first.Fingerprint, second.Fingerprint, second.Sources)
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

func TestIntegrationCandidateFingerprintExcludesTitleAndCaptureMetadata(t *testing.T) {
	ref := model.EvidenceRef{Kind: "integration", Provider: "codex", Ref: "mcp://ci:build-7", Connection: integrationContextRef, URL: "https://ci.example/build/7", Revision: "rev-1", Summary: "Build failure", AttemptID: "attempt-1", RetrievedAt: "2026-10-22T10:00:00Z"}
	candidate := func() []model.Direction {
		return []model.Direction{{ID: "integration-proposal", ProjectID: "p", Title: "Fix build failure", Description: "Investigate CI regression", Problem: "CI build is failing", Mechanism: "Repair the failing build path", Components: []string{"ci/build"}, ExpectedBenefit: "Restore builds", EffortEstimate: "Small", ProblemKind: "opportunity", Uncertainty: []string{"Confirm cause"}, Evidence: []model.EvidenceRef{ref}}}
	}
	first := candidate()
	if err := validateCandidateEvidence(first, discoverySnapshot{Commit: "repo-rev", Evidence: []model.EvidenceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	ref.AttemptID, ref.RetrievedAt = "attempt-2", "2026-10-22T11:00:00Z"
	second := candidate()
	if err := validateCandidateEvidence(second, discoverySnapshot{Commit: "repo-rev", Evidence: []model.EvidenceRef{ref}}); err != nil {
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
	if err := validateCandidateEvidence(third, discoverySnapshot{Commit: "repo-rev", Evidence: []model.EvidenceRef{ref}}); err != nil {
		t.Fatal(err)
	}
	if third[0].Fingerprint != second[0].Fingerprint || third[0].Evidence[0].Revision != "rev-2" {
		t.Fatal("evidence revision changed proposal identity or failed to persist new evidence")
	}
}
