package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const nativeContractRepository = "https://forge.example/team/repo"
const nativeContractPR = "https://forge.example/team/repo/pull/7"
const nativeContractBranch = "codex/change"

func TestIntegrationPRResultRequiresExactDestination(t *testing.T) {
	intent := model.Publication{Branch: nativeContractBranch, Base: "main", Head: "reviewed-sha"}
	for _, tc := range []struct{ repository, address string }{
		{nativeContractRepository, nativeContractPR},
		{"https://gitlab.example/team/repo", "https://gitlab.example/team/repo/-/merge_requests/7"},
		{"https://dev.azure.com/org/project/_git/repo", "https://dev.azure.com/org/project/_git/repo/pullrequest/7"},
	} {
		if got, err := checkedPRResult(nativePRResult{URL: tc.address, Head: intent.Branch, HeadSHA: intent.Head, Base: intent.Base, Repository: tc.repository}, intent, tc.repository); err != nil || got != tc.address {
			t.Fatalf("valid destination rejected: %s %v", got, err)
		}
	}
	for _, tc := range []struct{ name, address, repository, head, base string }{
		{"missing repository", nativeContractPR, "", intent.Branch, intent.Base},
		{"other repository", nativeContractPR, "https://forge.example/team/other", intent.Branch, intent.Base},
		{"different host", "https://other.example/team/repo/pull/7", nativeContractRepository, intent.Branch, intent.Base},
		{"different path", "https://forge.example/team/other/pull/7", nativeContractRepository, intent.Branch, intent.Base},
		{"repository prefix collision", "https://forge.example/team/repository/pull/7", nativeContractRepository, intent.Branch, intent.Base},
		{"path traversal", "https://forge.example/team/repo/../other/pull/7", nativeContractRepository, intent.Branch, intent.Base},
		{"repository homepage", nativeContractRepository, nativeContractRepository, intent.Branch, intent.Base},
		{"wrong head", nativeContractPR, nativeContractRepository, "other", intent.Base},
		{"wrong base", nativeContractPR, nativeContractRepository, intent.Branch, "other"},
		{"credentials", "https://secret@forge.example/team/repo/pull/7", nativeContractRepository, intent.Branch, intent.Base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := checkedPRResult(nativePRResult{URL: tc.address, Head: tc.head, HeadSHA: intent.Head, Base: tc.base, Repository: tc.repository}, intent, nativeContractRepository); err == nil {
				t.Fatal("mismatched PR destination accepted")
			}
		})
	}
}

func nativePublicationFixture(t *testing.T) (*Engine, model.Project, model.Task) {
	t.Helper()
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	p.RemoteURL = nativeContractRepository
	r, err := e.Store.Get(ctx, "providers", "fake")
	if err != nil {
		t.Fatal(err)
	}
	configured, err := store.Decode[model.Provider](r)
	if err != nil {
		t.Fatal(err)
	}
	configured.Kind = "codex"
	if err := e.Store.Put(ctx, "providers", configured.ID, configured, r.Version); err != nil {
		t.Fatal(err)
	}
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode, Integrations: model.IntegrationSettings{Publication: map[string]model.IntegrationTarget{p.RemoteURL: {Provider: "fake", Ref: agenticFixtureConnection}}}}
	if err := e.Store.Put(ctx, "workflow_config", "global", config, 0); err != nil {
		t.Fatal(err)
	}
	task.Mode = model.AgenticMode
	prepareNativeFixtureCapabilities(t, e, p, task)
	return e, p, task
}

func nativeFixtureCatalog() []model.Integration {
	return []model.Integration{{Provider: "fake", Ref: agenticFixtureConnection, Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "repo_info", ReadOnly: true}, {Name: "find_pr", ReadOnly: true}, {Name: "open_pr"}}}}
}

func prepareNativeFixtureCapabilities(t *testing.T, e *Engine, project model.Project, task model.Task) {
	t.Helper()
	previous := e.Adapter
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			text, _ := json.Marshal(map[string]string{"repository": project.RemoteURL, "default_branch": "main", "lookup_pr_tool": "find_pr", "create_pr_tool": "open_pr"})
			return provider.Output{Text: string(text), Integrations: nativeFixtureCatalog(), ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "repo_info", Success: true}}}, nil
		})
	}
	t.Cleanup(func() { e.Adapter = previous })
	if err := e.PreflightPublication(context.Background(), project, task); err != nil {
		t.Fatal(err)
	}
	e.Adapter = previous
}

func TestPublicationPreflightChecksNativeCapabilitiesAndReadEvidence(t *testing.T) {
	for _, tc := range []struct{ name, defect string }{
		{"valid", ""}, {"missing lookup", "lookup"}, {"missing create", "create"}, {"lookup writes", "lookup-write"}, {"create read-only", "create-read"}, {"disabled", "disabled"}, {"disconnected", "disconnected"}, {"missing names", "names"}, {"wrong repository", "repository"}, {"write during preflight", "write-evidence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, p, task := nativePublicationFixture(t)
			output := nativePreflightResponse(p, tc.defect)
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
					if input.Role != "submit" || input.Purpose != "integration:repository_metadata" || !input.ReadOnly || len(input.RequiredIntegrations) != 1 || input.RequiredIntegrations[0].Ref != agenticFixtureConnection {
						t.Fatalf("unsafe publication preflight: %+v", input)
					}
					return output, nil
				})
			}
			err := e.PreflightPublication(context.Background(), p, task)
			if tc.defect == "" && err != nil {
				t.Fatal(err)
			}
			if tc.defect != "" && err == nil {
				t.Fatal("unusable publication destination passed preflight")
			}
		})
	}
}

func nativePreflightResponse(p model.Project, defect string) provider.Output {
	tools := nativeFixtureCatalog()[0].Tools
	switch defect {
	case "lookup":
		tools = append(tools[:1], tools[2:]...)
	case "create":
		tools = tools[:2]
	case "lookup-write":
		tools[1].ReadOnly = false
	case "create-read":
		tools[2].ReadOnly = true
	}
	metadata := map[string]string{"repository": p.RemoteURL, "default_branch": "main", "lookup_pr_tool": "find_pr", "create_pr_tool": "open_pr"}
	if defect == "names" {
		delete(metadata, "create_pr_tool")
	}
	if defect == "repository" {
		metadata["repository"] = "https://forge.example/team/other"
	}
	text, _ := json.Marshal(metadata)
	catalog := []model.Integration{{Provider: "fake", Ref: agenticFixtureConnection, Enabled: defect != "disabled", Callable: defect != "disconnected", Tools: tools}}
	tool := "repo_info"
	if defect == "write-evidence" {
		tool = "open_pr"
	}
	return provider.Output{Text: string(text), Integrations: catalog, ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: tool, Success: true}}}
}

func TestPublicationLookupRequiresExplicitFoundBeforeCreate(t *testing.T) {
	for _, lookup := range []string{`{}`, `null`, `{"found":null}`, `{"repository":"https://forge.example/team/repo"}`} {
		t.Run(lookup, func(t *testing.T) {
			e, p, task := nativePublicationFixture(t)
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
					if input.Purpose != "integration:lookup_pr" {
						t.Fatalf("malformed lookup triggered write: %s", input.Purpose)
					}
					return provider.Output{Text: lookup, Integrations: nativeFixtureCatalog(), ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "find_pr", Success: true}}}, nil
				})
			}
			step := taskStep{Engine: e, ctx: context.Background(), project: p, task: task}
			_, err := step.publishIntegrationPR(model.Publication{Branch: nativeContractBranch, Base: "main", Head: "reviewed-sha"})
			if err == nil || !strings.Contains(err.Error(), "found") {
				t.Fatalf("malformed lookup did not block before git push/create: %v", err)
			}
		})
	}
}

func TestIntegrationPRResultRejectsStaleRemoteCommit(t *testing.T) {
	intent := model.Publication{Branch: nativeContractBranch, Base: "main", Head: "accepted-sha"}
	if _, err := checkedPRResult(nativePRResult{URL: nativeContractPR, Head: intent.Branch, HeadSHA: "different-sha", Base: intent.Base, Repository: nativeContractRepository}, intent, nativeContractRepository); err == nil {
		t.Fatal("published PR points at unreviewed commit")
	}
}

func TestNativePublicationRecoversLostCreateWithoutDuplicate(t *testing.T) {
	e, p, task := nativePublicationFixture(t)
	ctx := context.Background()
	nativeFixtureOrigin(t, p.Path, p.RemoteURL)
	task.Worktree = p.Path
	intent := model.Publication{Branch: nativeContractBranch, Base: "main", Head: submissionGit(t, p.Path, "rev-parse", "HEAD")}
	creates := 0
	found := false
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if in.Purpose == "integration:create_pr" {
				creates++
				found = true
				return provider.Output{}, errors.New("connection reset by peer after remote create")
			}
			if in.Purpose != "integration:lookup_pr" {
				t.Fatalf("unexpected operation %s", in.Purpose)
			}
			result := map[string]any{"found": found, "repository": p.RemoteURL}
			if found {
				result["url"] = p.RemoteURL + "/pull/7"
				result["head"] = intent.Branch
				result["head_sha"] = intent.Head
				result["base"] = intent.Base
			}
			text, _ := json.Marshal(result)
			return provider.Output{Text: string(text), Integrations: nativeFixtureCatalog(), ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "find_pr", Success: true}}}, nil
		})
	}
	step := taskStep{Engine: e, ctx: ctx, project: p, task: task}
	if _, err := step.publishIntegrationPR(intent); err == nil {
		t.Fatal("lost response accepted")
	}
	url, err := step.publishIntegrationPR(intent)
	if err != nil || url != p.RemoteURL+"/pull/7" || creates != 1 {
		t.Fatalf("uncertain result not reconciled: url=%s creates=%d err=%v", url, creates, err)
	}
}

func TestNativeRepositoryOperationRejectsPartialAndForeignEvidence(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			e, p, task := nativePublicationFixture(t)
			bad := provider.ToolEvidence{Ref: agenticFixtureConnection, Tool: "find_pr", Success: false}
			if foreign {
				bad.Ref = "mcp://other"
				bad.Success = true
			}
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					return provider.Output{Text: `{"found":false,"repository":"https://forge.example/team/repo"}`, ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "find_pr", Success: true}, bad}}, nil
				})
			}
			_, err := e.integrationOperation(context.Background(), p, task, nativeRepositoryOperation{Target: model.IntegrationTarget{Provider: "fake", Ref: agenticFixtureConnection}, Name: "lookup_pr", Arguments: map[string]string{"repository": p.RemoteURL}})
			if err == nil {
				t.Fatal("incomplete or cross-connection evidence accepted")
			}
		})
	}
}
