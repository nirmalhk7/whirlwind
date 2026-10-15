package settingsfile

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type configurationChanges struct {
	source *Source
	ctx    context.Context
	values []store.Change
}

func (s *Source) configurationChanges(ctx context.Context, doc Document) ([]store.Change, error) {
	c := &configurationChanges{source: s, ctx: ctx, values: []store.Change{}}
	if err := c.appendValue("settings", "global", doc.Workers); err != nil {
		return nil, err
	}
	for _, p := range doc.Providers {
		if err := c.provider(p); err != nil {
			return nil, err
		}
	}
	for role, chain := range doc.Agents {
		if err := c.appendValue("agents", role, model.AgentConfig{Role: role, Chain: chain}); err != nil {
			return nil, err
		}
	}
	if err := c.removals(doc); err != nil {
		return nil, err
	}
	return c.values, nil
}

func (c *configurationChanges) appendValue(kind, id string, value any) error {
	record, err := c.source.store.Get(c.ctx, kind, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil && equalValue(record.Data, value) {
		return nil
	}
	c.values = append(c.values, store.Change{Kind: kind, ID: id, Value: value, Expected: record.Version})
	return nil
}

func equalValue(data []byte, value any) bool {
	var existing, wanted any
	_ = json.Unmarshal(data, &existing)
	b, _ := json.Marshal(value)
	_ = json.Unmarshal(b, &wanted)
	return reflect.DeepEqual(existing, wanted)
}

func (c *configurationChanges) provider(p Provider) error {
	record, err := c.source.store.Get(c.ctx, "providers", p.ID)
	old := model.Provider{}
	if err == nil {
		old, err = store.Decode[model.Provider](record)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(project(old), p) {
			return nil
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	value := managed(p, old)
	if old.ID != "" && !reflect.DeepEqual(old.Command, p.Command) {
		value.Quota = "unknown"
		value.ResetAt = ""
	}
	return c.appendValue("providers", p.ID, value)
}

func (c *configurationChanges) removals(doc Document) error {
	desired := map[string]bool{}
	for _, p := range doc.Providers {
		desired[p.ID] = true
	}
	for _, kind := range []string{"providers", "agents"} {
		records, err := c.source.store.List(c.ctx, kind)
		if err != nil {
			return err
		}
		for _, record := range records {
			keep := desired[record.ID]
			if kind == "agents" {
				_, keep = doc.Agents[record.ID]
			}
			if !keep {
				c.values = append(c.values, store.Change{Kind: kind, ID: record.ID, Value: store.ConfigurationRemoval{}, Expected: record.Version})
			}
		}
	}
	return nil
}
