package settingsfile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (s *Source) ensureFile(ctx context.Context) error {
	_, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.seed(ctx)
	}
	return err
}

func (s *Source) seed(ctx context.Context) error {
	doc := Document{Version: 1, Workers: workflow.DefaultSettings(), Providers: []Provider{}, Agents: map[string][]model.AgentChoice{}}
	for _, load := range []func(context.Context, *Document) error{s.seedWorkers, s.seedProviders, s.seedAgents} {
		if err := load(ctx, &doc); err != nil {
			return err
		}
	}
	if err := validate(doc); err != nil {
		return err
	}
	encoded, err := encode(doc)
	if err != nil {
		return err
	}
	return s.createFile(encoded)
}

func (s *Source) seedWorkers(ctx context.Context, doc *Document) error {
	record, err := s.store.Get(ctx, "settings", "global")
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(record.Data, &doc.Workers)
}

func (s *Source) seedProviders(ctx context.Context, doc *Document) error {
	records, err := s.store.List(ctx, "providers")
	if err != nil {
		return err
	}
	for _, record := range records {
		p, err := store.Decode[model.Provider](record)
		if err != nil {
			return err
		}
		doc.Providers = append(doc.Providers, project(p))
	}
	return nil
}

func (s *Source) seedAgents(ctx context.Context, doc *Document) error {
	records, err := s.store.List(ctx, "agents")
	if err != nil {
		return err
	}
	for _, record := range records {
		config, err := store.Decode[model.AgentConfig](record)
		if err != nil {
			return err
		}
		doc.Agents[config.Role] = config.Chain
	}
	return nil
}

func (s *Source) createFile(encoded []byte) error {
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(err, syncErr, closeErr)
}
