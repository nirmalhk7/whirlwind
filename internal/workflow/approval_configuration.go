package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) approvalConfiguration(ctx context.Context, projectID string) ([]model.ConfigurationReference, []store.Change, error) {
	collector := approvalConfigurationCollector{engine: e, providers: map[string]bool{}}
	if err := collector.appendReference(ctx, "projects", projectID); err != nil {
		return nil, nil, err
	}
	for _, role := range model.AgentRoles() {
		if err := collector.appendAgent(ctx, role); err != nil {
			return nil, nil, err
		}
	}
	if err := collector.appendReference(ctx, "workflow_config", "global"); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}
	if err := collector.appendExternalQuality(ctx, projectID); err != nil {
		return nil, nil, err
	}
	return collector.references, collector.guards, nil
}

type approvalConfigurationCollector struct {
	engine     *Engine
	references []model.ConfigurationReference
	guards     []store.Change
	providers  map[string]bool
}

func (c *approvalConfigurationCollector) appendReference(ctx context.Context, kind, id string) error {
	r, err := c.engine.Store.Get(ctx, kind, id)
	if err != nil {
		return err
	}
	data, err := configurationData(kind, r.Data)
	if err != nil {
		return err
	}
	c.references = append(c.references, model.ConfigurationReference{Kind: kind, ID: id, Version: r.Version, Hash: evidenceDigest(json.RawMessage(data)), Data: data})
	c.guards = append(c.guards, store.VersionPrecondition(kind, id, r.Version))
	return nil
}

func (c *approvalConfigurationCollector) appendAgent(ctx context.Context, role string) error {
	if err := c.appendReference(ctx, "agents", role); err != nil {
		return fmt.Errorf("configure %s queue before approval: %w", role, err)
	}
	var config model.AgentConfig
	if err := json.Unmarshal(c.references[len(c.references)-1].Data, &config); err != nil {
		return err
	}
	if len(config.Chain) == 0 {
		return fmt.Errorf("configure %s queue before approval", role)
	}
	for _, choice := range config.Chain {
		if c.providers[choice.Provider] {
			continue
		}
		c.providers[choice.Provider] = true
		if err := c.appendReference(ctx, "providers", choice.Provider); err != nil {
			return err
		}
	}
	return nil
}

func (c *approvalConfigurationCollector) appendExternalQuality(ctx context.Context, projectID string) error {
	projectRecord, err := c.engine.Store.Get(ctx, "projects", projectID)
	if err != nil {
		return err
	}
	project, err := store.Decode[model.Project](projectRecord)
	if err != nil {
		return err
	}
	if externalQualityConfigured(project) {
		snapshot, err := externalQualitySnapshot(project)
		if err != nil {
			return err
		}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		c.references = append(c.references, model.ConfigurationReference{Kind: "quality_files", ID: projectID, Version: projectRecord.Version, Hash: evidenceDigest(json.RawMessage(data)), Data: data})
	}
	return nil
}

// Runtime quota/availability and project pauses do not change execution commands.
func configurationData(kind string, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	switch kind {
	case "projects":
		var p model.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		value = struct {
			ID, Path, Repo, PolicyHash string
			Policy                     model.Policy
		}{p.ID, p.Path, p.Repo, p.PolicyHash, p.Policy}
	case "providers":
		var p model.Provider
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		value = struct {
			ID, Kind, ACPProfile, QuotaPool string
			Command, ModelCommand           []string
			Sandbox                         *model.SandboxPolicy
		}{p.ID, p.Kind, p.ACPProfile, p.QuotaPool, p.Command, p.ModelCommand, p.Sandbox}
	default:
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(value)
	return data, err
}

func (e *Engine) validateApprovalConfiguration(ctx context.Context, plan model.ExecutionPlan) error {
	if plan.Plan == nil || plan.Plan.Approval == nil {
		return nil
	}
	approval := plan.Plan.Approval
	if plan.PlanDocument == nil || approval.PlanVersion != plan.PlanVersion || approval.DocumentHash != plan.PlanDocument.ApprovedHash || approval.ConfigurationHash != evidenceDigest(approval.References) {
		return errors.New("approved configuration identity is invalid")
	}
	for _, ref := range approval.References {
		if err := e.validateApprovedConfigurationReference(ctx, plan.ProjectID, ref); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) validateApprovedConfigurationReference(ctx context.Context, projectID string, ref model.ConfigurationReference) error {
	if ref.Kind == "quality_files" {
		return e.validateApprovedQualityReference(ctx, projectID, ref)
	}
	record, err := e.Store.Get(ctx, ref.Kind, ref.ID)
	if err != nil {
		return fmt.Errorf("approved configuration %s/%s unavailable: %w", ref.Kind, ref.ID, err)
	}
	data, err := configurationData(ref.Kind, record.Data)
	if err != nil {
		return err
	}
	if evidenceDigest(json.RawMessage(data)) != ref.Hash {
		return fmt.Errorf("approved configuration %s/%s changed; review and approve a new plan version", ref.Kind, ref.ID)
	}
	return nil
}

func (e *Engine) validateApprovedQualityReference(ctx context.Context, projectID string, ref model.ConfigurationReference) error {
	if ref.ID != projectID {
		return errors.New("external quality configuration project mismatch")
	}
	project, err := e.Project(ctx, projectID)
	if err != nil {
		return err
	}
	snapshot, err := externalQualitySnapshot(project)
	if err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if evidenceDigest(json.RawMessage(data)) != ref.Hash {
		return errors.New("approved external quality configuration changed; review and approve a new plan version")
	}
	return nil
}
