package workflow

import (
	"context"
	"errors"
	"fmt"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) ideationConfigurationHash(ctx context.Context, project model.Project) (string, error) {
	record, err := e.Store.Get(ctx, "agents", "ideate")
	if err != nil {
		return "", err
	}
	config, err := store.Decode[model.AgentConfig](record)
	if err != nil {
		return "", err
	}
	type providerConfiguration struct {
		ID, Kind, ACPProfile, QuotaPool string
		Command, ModelCommand           []string
		Sandbox                         *model.SandboxPolicy
	}
	var providers []providerConfiguration
	seen := map[string]bool{}
	for _, choice := range config.Chain {
		if seen[choice.Provider] {
			continue
		}
		seen[choice.Provider] = true
		record, err := e.Store.Get(ctx, "providers", choice.Provider)
		if err != nil {
			return "", err
		}
		provider, err := store.Decode[model.Provider](record)
		if err != nil {
			return "", err
		}
		providers = append(providers, providerConfiguration{provider.ID, provider.Kind, provider.ACPProfile, provider.QuotaPool, provider.Command, provider.ModelCommand, provider.Sandbox})
	}
	return evidenceDigest([]any{project.PolicyHash, project.Policy, config, providers, e.settings(ctx).Ideation, e.WorkflowConfiguration(ctx)}), nil
}

func (e *Engine) pinIdeationRunIdentity(ctx context.Context, project model.Project) error {
	id := operationID(ctx)
	if id == "" {
		return nil
	}
	record, err := e.Store.Get(ctx, "operations", id)
	if err != nil {
		return err
	}
	op, err := store.Decode[model.Operation](record)
	if err != nil {
		return err
	}
	if op.Kind != "ideation" || op.Subject != project.ID || op.Stage != "running" {
		return store.ErrConflict
	}
	revision, err := github.Run(ctx, project.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	configuration, err := e.ideationConfigurationHash(ctx, project)
	if err != nil {
		return err
	}
	if op.RepositoryRevision != "" && op.RepositoryRevision != revision || op.ConfigurationHash != "" && op.ConfigurationHash != configuration {
		return errors.New("ideation repository or configuration changed; start a distinct new run")
	}
	if op.RepositoryRevision == revision && op.ConfigurationHash == configuration {
		return nil
	}
	op.RepositoryRevision, op.ConfigurationHash = revision, configuration
	return e.Store.Put(ctx, "operations", id, op, record.Version)
}

func (e *Engine) validateIdeationRunIdentity(ctx context.Context, project model.Project, revision string) error {
	id := operationID(ctx)
	if id == "" {
		return nil
	}
	record, err := e.Store.Get(ctx, "operations", id)
	if err != nil {
		return err
	}
	op, err := store.Decode[model.Operation](record)
	if err != nil {
		return err
	}
	configuration, err := e.ideationConfigurationHash(ctx, project)
	if err != nil {
		return err
	}
	if op.Kind != "ideation" || op.Subject != project.ID || op.Stage != "running" || op.RepositoryRevision != revision || op.ConfigurationHash != configuration {
		return fmt.Errorf("ideation run %s repository/configuration identity changed before publication (stage=%s, pinned repository=%s, current repository=%s, pinned configuration=%s, current configuration=%s)", id, op.Stage, op.RepositoryRevision, revision, op.ConfigurationHash, configuration)
	}
	return nil
}
