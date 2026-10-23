package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

type publicationMetadata struct {
	DefaultBranch string `json:"default_branch"`
	Repository    string `json:"repository"`
	LookupPRTool  string `json:"lookup_pr_tool"`
	CreatePRTool  string `json:"create_pr_tool"`
}

func inspectedDefaultBranch(ctx context.Context, project model.Project, raw json.RawMessage) (string, error) {
	var metadata publicationMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return "", err
	}
	if metadata.DefaultBranch == "" || metadata.Repository != project.RemoteURL {
		return "", errors.New("integration returned incomplete or mismatched repository metadata")
	}
	if _, err := github.Run(ctx, project.Path, "git", "check-ref-format", "refs/heads/"+metadata.DefaultBranch); err != nil {
		return "", errors.New("integration returned invalid default branch")
	}
	return metadata.DefaultBranch, nil
}

// PreflightPublication verifies exact native destination capabilities before
// implementation. Only witnessed read-only tools may run during preflight.
func (e *Engine) PreflightPublication(ctx context.Context, p model.Project, task model.Task) error {
	target, err := e.publicationTarget(ctx, p)
	if err != nil {
		return err
	}
	_, err = e.integrationOperation(ctx, p, task, nativeRepositoryOperation{Target: target, Name: "repository_metadata", Arguments: map[string]string{"repository": p.RemoteURL}})
	return err
}

func validatePublicationMetadata(raw json.RawMessage, repository string, target model.IntegrationTarget, output provider.Output) error {
	metadata, err := decodePublicationMetadata(raw, repository)
	if err != nil {
		return err
	}
	tools, err := selectedPublicationTools(output.Integrations, target)
	if err != nil {
		return err
	}
	if err := validatePublicationTools(metadata, tools); err != nil {
		return err
	}
	return validatePreflightCalls(output.ToolCalls, target, tools)
}

func decodePublicationMetadata(raw json.RawMessage, repository string) (publicationMetadata, error) {
	var metadata publicationMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return metadata, fmt.Errorf("invalid publication repository metadata: %w", err)
	}
	if metadata.DefaultBranch == "" || metadata.Repository != repository {
		return metadata, errors.New("publication metadata must identify the exact canonical repository and default branch; refresh the configured integration")
	}
	if metadata.LookupPRTool == "" || metadata.CreatePRTool == "" || metadata.LookupPRTool == metadata.CreatePRTool {
		return metadata, errors.New("publication metadata requires distinct lookup_pr_tool and create_pr_tool names from the selected native integration")
	}
	return metadata, nil
}

func selectedPublicationTools(catalog []model.Integration, target model.IntegrationTarget) (map[string]bool, error) {
	for _, item := range catalog {
		if !callablePublicationConnection(item, target) {
			continue
		}
		tools := map[string]bool{}
		for _, tool := range item.Tools {
			tools[tool.Name] = tool.ReadOnly
		}
		return tools, nil
	}
	return nil, errors.New("publication connection is disabled, disconnected, or missing from native inventory; reconnect and refresh")
}

func callablePublicationConnection(item model.Integration, target model.IntegrationTarget) bool {
	return item.Provider == target.Provider && item.Ref == target.Ref && item.Enabled && item.Callable
}

func validatePublicationTools(metadata publicationMetadata, tools map[string]bool) error {
	if readOnly, exists := tools[metadata.LookupPRTool]; !exists || !readOnly {
		return errors.New("publication lookup_pr_tool must name an available read-only native tool on the configured connection")
	}
	if readOnly, exists := tools[metadata.CreatePRTool]; !exists || readOnly {
		return errors.New("publication create_pr_tool must name an available write native tool on the configured connection")
	}
	return nil
}

func validatePreflightCalls(calls []provider.ToolEvidence, target model.IntegrationTarget, tools map[string]bool) error {
	for _, call := range calls {
		readOnly, exists := tools[call.Tool]
		if call.Ref != target.Ref || !exists || !readOnly {
			return errors.New("publication preflight requires read-only tool evidence from the selected connection; write or unknown tools are forbidden")
		}
	}
	return nil
}

type nativePRResult struct {
	Found                       *bool `json:"found"`
	URL, Head, Base, Repository string
	HeadSHA                     string `json:"head_sha"`
}

func (step *taskStep) publishIntegrationPR(intent model.Publication) (string, error) {
	target, err := step.publicationTarget(step.ctx, step.project)
	if err != nil {
		return "", err
	}
	result, err := step.lookupIntegrationPR(intent, target)
	if err != nil {
		return "", err
	}
	if *result.Found {
		return checkedPRResult(result, intent, step.project.RemoteURL)
	}
	return step.createIntegrationPR(intent, target, result)
}

func integrationPublicationArgs(repository string, intent model.Publication) map[string]string {
	return map[string]string{"repository": repository, "head": intent.Branch, "base": intent.Base, "title": intent.Title, "body": intent.Body, "head_sha": intent.Head}
}

func (step *taskStep) lookupIntegrationPR(intent model.Publication, target model.IntegrationTarget) (nativePRResult, error) {
	var result nativePRResult
	args := integrationPublicationArgs(step.project.RemoteURL, intent)
	raw, err := step.integrationOperation(step.ctx, step.project, step.task, nativeRepositoryOperation{Target: target, Name: "lookup_pr", Arguments: args})
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.Found == nil {
		return result, errors.New("native PR lookup result must include an explicit boolean found; no publication was attempted")
	}
	if result.Repository != step.project.RemoteURL {
		return result, errors.New("native PR lookup repository does not match the configured canonical destination")
	}
	return result, nil
}

func (step *taskStep) createIntegrationPR(intent model.Publication, target model.IntegrationTarget, result nativePRResult) (string, error) {
	if err := step.pushIntegrationCommit(intent); err != nil {
		return "", err
	}
	args := integrationPublicationArgs(step.project.RemoteURL, intent)
	raw, err := step.integrationOperation(step.ctx, step.project, step.task, nativeRepositoryOperation{Target: target, Name: "create_pr", Arguments: args})
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return checkedPRResult(result, intent, step.project.RemoteURL)
}

func checkedPRResult(result nativePRResult, intent model.Publication, expectedRepository string) (string, error) {
	u, err := url.Parse(result.URL)
	if err != nil || !validNativePRAddress(u) || !result.matches(intent) {
		return "", errors.New("integration PR result does not match intended repository branch and base")
	}
	canonical, err := CanonicalRemote(expectedRepository)
	if err != nil || result.Repository != canonical {
		return "", errors.New("integration PR result must identify the exact canonical publication repository")
	}
	remote, err := url.Parse(canonical)
	if err != nil || !nativePRBelongsToRepository(u, remote) {
		return "", errors.New("integration PR URL does not belong to the configured repository destination")
	}
	return result.URL, nil
}

func validNativePRAddress(address *url.URL) bool {
	return address != nil && address.Scheme == "https" && address.Host != "" && address.User == nil
}

func (result nativePRResult) matches(intent model.Publication) bool {
	head := result.Head == intent.Branch && result.HeadSHA == intent.Head
	base := result.Base == intent.Base && intent.Head != ""
	return head && base
}

func nativePRBelongsToRepository(address, repository *url.URL) bool {
	host := strings.EqualFold(address.Host, repository.Host) && path.Clean(address.Path) == address.Path
	prefix := repository.Path + "/"
	location := strings.HasPrefix(address.Path, prefix) && strings.TrimPrefix(address.Path, prefix) != ""
	return host && location
}
