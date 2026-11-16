package workflow

import (
	"context"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func providerCredentialFixture() model.NetworkCredential {
	return model.NetworkCredential{Upstream: "https://api.example.com", CredentialKey: "env://FIXTURE_API_KEY", EnvVar: "FIXTURE_API_KEY", InjectHeader: "Authorization", CredentialFormat: "Bearer {}", EndpointRules: []model.NetworkEndpointRule{{Method: "POST", Path: "/v1/messages"}}}
}

func TestParityWorkflowUsesHumanConfiguredProviderSandbox(t *testing.T) {
	e, plan, cwd := testEngine(t)
	project, err := e.Project(context.Background(), plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	chosen := model.Provider{ID: "configured", Kind: "acp", Sandbox: &model.SandboxPolicy{AllowedDomains: []string{"https://api.example.com/v1/**"}, Credentials: map[string]model.NetworkCredential{"fixture": providerCredentialFixture()}}}
	run := agentRun{Engine: e, ctx: context.Background(), project: project, task: plan, input: provider.Input{Role: "code", CWD: cwd}}
	if err := run.prepareExecutionProfile(chosen); err != nil {
		t.Fatal(err)
	}
	profile := run.input.Execution
	if profile == nil || profile.Credentials["fixture"].CredentialKey != "env://FIXTURE_API_KEY" {
		t.Fatal("normal workflow discarded selected provider's human credential configuration")
	}
	if len(profile.Domains) != 1 || profile.Domains[0] != "https://api.example.com/v1/**" {
		t.Fatal("normal workflow discarded selected provider's network scope")
	}
}

func TestParityConflictingProjectAndProviderCredentialBindingsFailClosed(t *testing.T) {
	e, plan, cwd := testEngine(t)
	project, err := e.Project(context.Background(), plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy.Sandbox = &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{"fixture": providerCredentialFixture()}}
	other := providerCredentialFixture()
	other.CredentialKey = "env://OTHER_API_KEY"
	chosen := model.Provider{ID: "configured", Kind: "acp", Sandbox: &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{"fixture": other}}}
	run := agentRun{Engine: e, ctx: context.Background(), project: project, task: plan, input: provider.Input{Role: "code", CWD: cwd}}
	if err := run.prepareExecutionProfile(chosen); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting credential binding silently selected: %v", err)
	}
}
