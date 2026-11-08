package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func CanonicalRemote(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", errors.New("invalid Git remote")
		}
		remote = "https://" + parts[0] + "/" + parts[1]
	}
	u, err := url.Parse(remote)
	if err != nil || u.Hostname() == "" || u.Path == "" {
		return "", errors.New("repository origin must identify a hosted Git repository")
	}
	u.Scheme = "https"
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	return u.String(), nil
}
func (e *Engine) publicationTarget(ctx context.Context, p model.Project) (model.IntegrationTarget, error) {
	config := e.WorkflowConfiguration(ctx)
	key := p.RemoteURL
	if key == "" {
		remote, err := github.Run(ctx, p.Path, "git", "remote", "get-url", "origin")
		if err != nil {
			return model.IntegrationTarget{}, err
		}
		key, err = CanonicalRemote(remote)
		if err != nil {
			return model.IntegrationTarget{}, err
		}
	}
	target, ok := config.Integrations.Publication[key]
	if !ok || target.Provider == "" || target.Ref == "" {
		return target, fmt.Errorf("configure integrations.publication[%q] with provider and native integration ref in settings.yaml", key)
	}
	return target, nil
}

type nativeRepositoryOperation struct {
	Target    model.IntegrationTarget
	Name      string
	Arguments any
	Setup     bool
}

func (e *Engine) integrationOperation(ctx context.Context, p model.Project, t model.Task, operation nativeRepositoryOperation) (json.RawMessage, error) {
	input, err := e.nativeRepositoryInput(ctx, p, t, operation)
	if err != nil {
		return nil, err
	}
	out, err := e.executeRepositoryOperation(ctx, p, t, input, operation.Setup)
	if err != nil {
		return nil, err
	}
	return e.checkedIntegrationOperation(ctx, p.RemoteURL, operation, out)
}

func nativeOperationInput(p model.Project, t model.Task, operation nativeRepositoryOperation) provider.Input {
	prompt := "Use only the explicitly selected native integration. Never invoke gh, curl, shell network clients, or a different connection. Perform operation " + operation.Name + " for repository " + p.RemoteURL + ". Return ONLY JSON matching operation contract below, derived from actual successful native tool results. Do not invent data or URLs. lookup_pr: {found:boolean,repository:string,url?:string,head?:string,head_sha?:string,base?:string}; create_pr: {url:string,head:string,head_sha:string,base:string,repository:string}; pr_state: {state:OPEN|CLOSED|MERGED}; repository_metadata: {default_branch:string,repository:string,lookup_pr_tool:string,create_pr_tool:string}. repository must be the exact canonical repository URL. Metadata discovery is read-only: return exact native tool names for PR lookup and creation without invoking any write tool. Names must come from the selected connection inventory, with lookup read-only and creation a write tool. For lookup_pr, search the exact repository/head/base and return the existing PR even if an earlier create timed out. create_pr must first recheck existing head/base to avoid duplicates. Never merge or close a PR.\nConnection: " + jsonText(operation.Target) + "\nArguments: " + jsonText(operation.Arguments)
	input := provider.Input{Role: "finalizer", Purpose: "integration:" + operation.Name, CWD: p.Path, Prompt: prompt, ReadOnly: operation.Name != "create_pr", NativeIntegrations: true, RequiredIntegrations: []model.IntegrationTarget{operation.Target}}
	if t.Worktree != "" {
		input.CWD = t.Worktree
	}
	return input
}

func (e *Engine) executeRepositoryOperation(ctx context.Context, p model.Project, task model.Task, input provider.Input, setup bool) (provider.Output, error) {
	if setup {
		run := &agentRun{Engine: e, ctx: store.Unstaged(ctx), project: p, task: task, input: input}
		return run.run()
	}
	return e.agent(ctx, p, task, input)
}

func validateNativeOperationEvidence(output provider.Output, target model.IntegrationTarget) error {
	if len(output.ToolCalls) == 0 {
		return errors.New("integration returned no native tool evidence; inspect provider connection and retry")
	}
	for _, call := range output.ToolCalls {
		if call.Ref != target.Ref {
			return errors.New("integration operation returned native tool evidence from a different connection; inspect selected connection and retry")
		}
		if !call.Success {
			return errors.New("integration operation includes a failed native tool result; inspect provider connection and retry")
		}
	}
	return nil
}

func (e *Engine) InspectIntegrationRepository(ctx context.Context, p model.Project) (string, error) {
	target, err := e.publicationTarget(ctx, p)
	if err != nil {
		return "", err
	}
	raw, err := e.integrationOperation(ctx, p, model.Task{ID: "onboard-" + p.ID, ProjectID: p.ID, Mode: model.AgenticMode, PlanVersion: 1, Issue: model.Issue{Title: "Inspect repository metadata"}}, nativeRepositoryOperation{Target: target, Name: "repository_metadata", Arguments: map[string]string{"repository": p.RemoteURL}, Setup: true})
	if err != nil {
		return "", err
	}
	return inspectedDefaultBranch(ctx, p, raw)
}
func (e *Engine) reconcileAgenticProject(ctx context.Context, p model.Project) {
	// Legacy publication records remain stored as historical data. Local-only
	// delivery does not poll remote pull requests or infer a human decision.
}

func integrationTaskNeedsObservation(task model.Task, projectID string) bool {
	sameProject := task.ProjectID == projectID
	hasPR := task.PR != ""
	active := !task.Cancelled() && task.Stage != "complete"
	return sameProject && hasPR && active
}

func (e *Engine) observeIntegrationOutcome(ctx context.Context, p model.Project, task model.Task, version int) {
	target, err := e.publicationTarget(ctx, p)
	if err != nil {
		_ = e.Store.Notify(ctx, task.ID, "integration_setup", err.Error())
		return
	}
	raw, err := e.integrationOperation(ctx, p, task, nativeRepositoryOperation{Target: target, Name: "pr_state", Arguments: map[string]string{"url": task.PR}})
	if err != nil {
		_ = e.Store.Notify(ctx, task.ID, "integration_error", err.Error())
		return
	}
	var state struct{ State string }
	if json.Unmarshal(raw, &state) == nil && (state.State == "MERGED" || state.State == "CLOSED") {
		task.Stage = "complete"
		_ = e.Store.Write(ctx, []store.Change{{Kind: "tasks", ID: task.ID, Value: task, Expected: version}, {Kind: "outcomes", ID: store.ID(), Value: map[string]any{"task": task.ID, "pr": task.PR, "human_decision": strings.ToLower(state.State), "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}}}, nil)
	}
}
func (e *Engine) IntegrationInventory(ctx context.Context, providerID string) ([]model.Integration, error) {
	record, err := e.Store.Get(ctx, "providers", providerID)
	if err != nil {
		return nil, err
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		return nil, err
	}
	p, cwd, err := e.integrationInventoryRuntime(ctx, providerID, p)
	if err != nil {
		return nil, err
	}
	// Metadata-only discovery invokes no model and does not need a task budget.
	output, err := e.Adapter(p).Run(ctx, provider.Input{ProviderID: providerID, NativeIntegrations: true, CatalogOnly: true, CWD: cwd, ReadOnly: true, Policy: model.Policy{Sandbox: p.Sandbox}}, provider.Hooks{})
	if err != nil {
		return nil, err
	}
	return output.Integrations, nil
}

func (e *Engine) integrationInventoryRuntime(ctx context.Context, providerID string, p model.Provider) (model.Provider, string, error) {
	projects, err := e.Store.List(ctx, "projects")
	if err != nil {
		return p, "", err
	}
	cwd := filepath.Join(e.Root, "integration-probes", providerID)
	if err = os.MkdirAll(cwd, 0700); err != nil {
		return p, "", err
	}
	if len(projects) > 0 {
		project, err := store.Decode[model.Project](projects[0])
		if err != nil {
			return p, "", err
		}
		cwd = project.Path
		if p.Sandbox == nil {
			p.Sandbox = project.Policy.Sandbox
		}
	}
	return p, cwd, nil
}
