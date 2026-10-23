package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const integrationCapabilitiesKind = "integration_capabilities"

type integrationCapabilities struct {
	Target   model.IntegrationTarget `json:"target"`
	Metadata publicationMetadata     `json:"metadata"`
}

func (e *Engine) checkedIntegrationOperation(ctx context.Context, repository string, operation nativeRepositoryOperation, out provider.Output) (json.RawMessage, error) {
	if err := validateNativeOperationEvidence(out, operation.Target); err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := provider.JSON(out.Text, &result); err != nil {
		return nil, err
	}
	if err := e.validateIntegrationCapabilities(ctx, repository, operation, out, result); err != nil {
		return nil, err
	}
	return result, nil
}

func nativePublicationOperation(name string) bool {
	return name == "lookup_pr" || name == "create_pr"
}

func (e *Engine) nativeRepositoryInput(ctx context.Context, project model.Project, task model.Task, operation nativeRepositoryOperation) (provider.Input, error) {
	input := nativeOperationInput(project, task, operation)
	if !nativePublicationOperation(operation.Name) {
		return input, nil
	}
	capabilities, err := e.readIntegrationCapabilities(ctx, project.RemoteURL, operation.Target)
	if err != nil {
		return input, err
	}
	input.Prompt += "\nValidated PR capability mapping: " + jsonText(capabilities.Metadata)
	return input, nil
}

func (e *Engine) validateIntegrationCapabilities(ctx context.Context, repository string, operation nativeRepositoryOperation, out provider.Output, result json.RawMessage) error {
	if operation.Name == "repository_metadata" {
		if err := validatePublicationMetadata(result, repository, operation.Target, out); err != nil {
			return err
		}
		return e.saveIntegrationCapabilities(ctx, repository, operation.Target, result)
	}
	if !nativePublicationOperation(operation.Name) {
		return nil
	}
	capabilities, err := e.readIntegrationCapabilities(ctx, repository, operation.Target)
	if err != nil {
		return err
	}
	return capabilities.validate(out, operation.Name)
}

func integrationCapabilitiesKey(repository string, target model.IntegrationTarget) (string, error) {
	canonical, err := CanonicalRemote(repository)
	if err != nil {
		return "", err
	}
	identity := struct {
		Repository string
		Target     model.IntegrationTarget
	}{canonical, target}
	digest := sha256.Sum256([]byte(jsonText(identity)))
	return hex.EncodeToString(digest[:]), nil
}

func (e *Engine) saveIntegrationCapabilities(ctx context.Context, repository string, target model.IntegrationTarget, raw json.RawMessage) error {
	metadata, err := decodePublicationMetadata(raw, repository)
	if err != nil {
		return err
	}
	key, err := integrationCapabilitiesKey(repository, target)
	if err != nil {
		return err
	}
	return e.storeIntegrationCapabilities(ctx, key, integrationCapabilities{Target: target, Metadata: metadata})
}

func (e *Engine) storeIntegrationCapabilities(ctx context.Context, key string, intended integrationCapabilities) error {
	record, err := e.Store.Get(ctx, integrationCapabilitiesKind, key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil {
		identical, decodeErr := sameIntegrationCapabilities(record, intended)
		if decodeErr != nil || identical {
			return decodeErr
		}
	}
	err = e.Store.Put(ctx, integrationCapabilitiesKind, key, intended, record.Version)
	if errors.Is(err, store.ErrConflict) {
		return e.concurrentIntegrationCapabilities(ctx, key, intended)
	}
	return err
}

func sameIntegrationCapabilities(record model.Record, intended integrationCapabilities) (bool, error) {
	existing, err := store.Decode[integrationCapabilities](record)
	return existing == intended, err
}

func (e *Engine) concurrentIntegrationCapabilities(ctx context.Context, key string, intended integrationCapabilities) error {
	record, err := e.Store.Get(ctx, integrationCapabilitiesKind, key)
	if err != nil {
		return errors.Join(store.ErrConflict, err)
	}
	identical, err := sameIntegrationCapabilities(record, intended)
	if err != nil || !identical {
		return errors.Join(store.ErrConflict, err)
	}
	return nil
}

func (e *Engine) readIntegrationCapabilities(ctx context.Context, repository string, target model.IntegrationTarget) (integrationCapabilities, error) {
	key, err := integrationCapabilitiesKey(repository, target)
	if err != nil {
		return integrationCapabilities{}, err
	}
	record, err := e.Store.Get(ctx, integrationCapabilitiesKind, key)
	if errors.Is(err, store.ErrNotFound) {
		return integrationCapabilities{}, errors.New("publication capabilities are missing for the configured repository and connection; run publication preflight before retrying")
	}
	if err != nil {
		return integrationCapabilities{}, err
	}
	capabilities, err := store.Decode[integrationCapabilities](record)
	if err == nil && (capabilities.Target != target || capabilities.Metadata.Repository != repository) {
		err = errors.New("publication capability mapping does not match the configured repository and connection; refresh publication preflight")
	}
	return capabilities, err
}

func (capabilities integrationCapabilities) validate(out provider.Output, operation string) error {
	tools, err := selectedPublicationTools(out.Integrations, capabilities.Target)
	if err != nil {
		return err
	}
	if err := validatePublicationTools(capabilities.Metadata, tools); err != nil {
		return err
	}
	return capabilities.validateWitnesses(out.ToolCalls, operation, tools)
}

func (capabilities integrationCapabilities) validateWitnesses(calls []provider.ToolEvidence, operation string, tools map[string]bool) error {
	witnessed := false
	for _, call := range calls {
		readOnly, exists := tools[call.Tool]
		if !exists || operation == "lookup_pr" && !readOnly {
			return errors.New("publication operation includes an unknown or disallowed native tool; refresh the configured connection")
		}
		lookup := call.Tool == capabilities.Metadata.LookupPRTool
		create := operation == "create_pr" && call.Tool == capabilities.Metadata.CreatePRTool
		witnessed = witnessed || lookup || create
	}
	if !witnessed {
		return errors.New("publication operation requires successful native evidence from its mapped PR lookup or creation tool")
	}
	return nil
}
