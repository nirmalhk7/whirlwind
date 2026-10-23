package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestNativePublicationCapabilitySaveIsIdempotentUnderConcurrency(t *testing.T) {
	e, project, _ := nativePublicationFixture(t)
	repository := project.RemoteURL + "-concurrent"
	target := model.IntegrationTarget{Provider: "fake", Ref: agenticFixtureConnection}
	raw, err := json.Marshal(publicationMetadata{Repository: repository, DefaultBranch: "main", LookupPRTool: "find_pr", CreatePRTool: "open_pr"})
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 12)
	for range cap(results) {
		go func() {
			<-start
			results <- e.saveIntegrationCapabilities(context.Background(), repository, target, raw)
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	key, err := integrationCapabilitiesKey(repository, target)
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(context.Background(), integrationCapabilitiesKind, key)
	if err != nil || record.Version != 1 {
		t.Fatalf("identical capability preflight rewrote its mapping: version=%d err=%v", record.Version, err)
	}
}

func TestNativePublicationRequiresMappedToolEvidence(t *testing.T) {
	for _, operation := range []string{"lookup_pr", "create_pr"} {
		t.Run(operation, func(t *testing.T) {
			e, project, task := nativePublicationFixture(t)
			catalog := []model.Integration{{Provider: "fake", Ref: agenticFixtureConnection, Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "repo_info", ReadOnly: true}, {Name: "find_pr", ReadOnly: true}, {Name: "open_pr"}}}}
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
					result := map[string]any{"found": false, "repository": project.RemoteURL}
					if input.Purpose == "integration:repository_metadata" {
						result = map[string]any{"repository": project.RemoteURL, "default_branch": "main", "lookup_pr_tool": "find_pr", "create_pr_tool": "open_pr"}
					}
					text, _ := json.Marshal(result)
					return provider.Output{Text: string(text), Integrations: catalog, ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: "repo_info", Success: true}}}, nil
				})
			}
			ctx := context.Background()
			if err := e.PreflightPublication(ctx, project, task); err != nil {
				t.Fatal(err)
			}
			_, err := e.integrationOperation(ctx, project, task, nativeRepositoryOperation{Target: model.IntegrationTarget{Provider: "fake", Ref: agenticFixtureConnection}, Name: operation, Arguments: map[string]string{"repository": project.RemoteURL}})
			if err == nil {
				t.Fatal("unrelated native read authorized PR lookup/creation")
			}
		})
	}
}
