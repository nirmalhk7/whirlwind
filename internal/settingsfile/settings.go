package settingsfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

type Provider struct {
	Sandbox      *model.SandboxPolicy `json:"sandbox,omitempty"`
	QuotaPool    string               `json:"quota_pool,omitempty"`
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	Command      []string             `json:"command"`
	ModelCommand []string             `json:"model_command,omitempty"`
	Capabilities []string             `json:"capabilities"`
	Cost         *float64             `json:"cost,omitempty"`
}
type Document struct {
	Mode         string                         `json:"mode,omitempty"`
	Integrations model.IntegrationSettings      `json:"integrations,omitempty"`
	Version      int                            `json:"version"`
	Workers      workflow.Settings              `json:"workers"`
	Providers    []Provider                     `json:"providers"`
	Agents       map[string][]model.AgentChoice `json:"agents"`
}
type Source struct {
	activeMode string
	startup    FileView
	mu         sync.Mutex
	store      *store.Store
	path       string
	raw        []byte
	doc        Document
}

func project(p model.Provider) Provider {
	return Provider{ID: p.ID, Name: p.Name, Kind: p.Kind, Command: p.Command, ModelCommand: p.ModelCommand, Capabilities: p.Capabilities, Cost: p.Cost, QuotaPool: p.QuotaPool, Sandbox: p.Sandbox}
}
func managed(p Provider, previous model.Provider) model.Provider {
	previous.ID = p.ID
	previous.QuotaPool = p.QuotaPool
	previous.Sandbox = p.Sandbox
	previous.Name = p.Name
	previous.Kind = p.Kind
	previous.Command = p.Command
	previous.ModelCommand = p.ModelCommand
	previous.Capabilities = p.Capabilities
	previous.Cost = p.Cost
	previous.Available = provider.Available(previous)
	if previous.Quota == "" {
		previous.Quota = "unknown"
	}
	return previous
}

// Attach imports an existing file or seeds it once from saved configuration.
func Attach(ctx context.Context, s *store.Store, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	source := &Source{store: s, path: absolute}
	ctx = store.WithoutSettingsSource(ctx)
	if err = source.ensureFile(ctx); err != nil {
		return err
	}
	raw, readErr := os.ReadFile(source.path)
	if readErr != nil {
		return readErr
	}
	initial, decodeErr := decode(raw)
	if decodeErr != nil {
		return decodeErr
	}
	source.activeMode = model.EffectiveMode(initial.Mode)
	if err = s.ActivateMode(ctx, source.activeMode); err != nil {
		return err
	}
	if err = source.Refresh(ctx); err != nil {
		return err
	}
	source.startup = FileView{Path: source.path, Content: string(source.raw), LoadedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	s.Source = source
	return nil
}
func validate(doc Document) error {
	if err := model.ValidateMode(doc.Mode); err != nil {
		return err
	}
	if doc.Integrations.Discovery != "" && doc.Integrations.Discovery != "connected" {
		return errors.New("integrations.discovery must be connected")
	}
	if doc.Version != 1 {
		return errors.New("settings.yaml version must be 1")
	}
	if err := validateWorkers(doc.Workers); err != nil {
		return err
	}
	providers, err := validateProviders(doc.Providers)
	if err != nil {
		return err
	}
	if err := validateIntegrations(doc.Integrations, providers); err != nil {
		return err
	}
	for role, chain := range doc.Agents {
		if err := validateChain(role, chain, providers); err != nil {
			return err
		}
	}
	return nil
}
func (s *Source) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh(ctx)
}
func (s *Source) refresh(ctx context.Context) error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("settings.yaml: %w", err)
	}
	doc := s.doc
	if !bytes.Equal(raw, s.raw) {
		doc, err = decode(raw)
		if err != nil {
			return fmt.Errorf("settings.yaml: %w", err)
		}
	}
	ctx = store.Unstaged(store.WithoutSettingsSource(ctx))
	changes, err := s.configurationChanges(ctx, doc)
	if err != nil {
		return err
	}
	if len(changes) > 0 {
		if err = s.store.Write(ctx, changes, nil); err != nil {
			return err
		}
	}
	s.doc = doc
	s.raw = append([]byte{}, raw...)
	return nil
}
func (s *Source) Persist(ctx context.Context, changes []store.Change, commit func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refresh(ctx); err != nil {
		return err
	}
	encoded, _ := json.Marshal(s.doc)
	var doc Document
	_ = json.Unmarshal(encoded, &doc)
	if doc.Agents == nil {
		doc.Agents = map[string][]model.AgentChoice{}
	}
	for _, c := range changes {
		if err := doc.apply(c); err != nil {
			return err
		}
	}
	if reflect.DeepEqual(s.doc, doc) {
		return commit()
	}
	return s.persistDocument(doc, commit)
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	_, err = f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(err, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(name, path)
}
