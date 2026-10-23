package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/provider"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/store"
)

func (c config) openState() (s *store.Store, lock *os.File, err error) {
	if err = os.MkdirAll(c.data, 0700); err != nil {
		return nil, nil, err
	}
	lock, err = process.Lock(filepath.Join(c.data, "server.lock"))
	if err != nil {
		return nil, nil, fmt.Errorf("another Whirlwind process owns data directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = lock.Close()
		}
	}()
	if err = c.prepareRegistry(); err != nil {
		return nil, lock, err
	}
	s, err = store.Open(filepath.Join(c.data, "whirlwind.db"))
	return s, lock, err
}
func (c config) prepareRegistry() error {
	registry := filepath.Join(c.data, "processes")
	if err := process.Reap(registry); err != nil {
		return err
	}
	return os.Setenv("WHIRLWIND_PROCESS_REGISTRY", registry)
}
func (c config) initialize(ctx context.Context, s *store.Store) error {
	bridge, err := filepath.Abs(c.bridge)
	if err != nil {
		return err
	}
	if err = c.initializeConfiguration(ctx, s, bridge); err != nil {
		return err
	}
	if err := s.RecoverInterrupted(ctx, "startup"); err != nil {
		return err
	}
	_ = s.Notify(ctx, "", "startup", "Whirlwind started; interrupted sessions require fresh lease before resumption")
	return nil
}

func (c config) initializeConfiguration(ctx context.Context, s *store.Store, bridge string) error {
	if err := initializeProviders(ctx, s, bridge); err != nil {
		return err
	}
	if err := initializeSettings(ctx, s); err != nil {
		return err
	}
	if c.settings == "" {
		return nil
	}
	return settingsfile.Attach(ctx, s, c.settings)
}
func initializeProviders(ctx context.Context, s *store.Store, bridge string) error {
	for _, p := range provider.Defaults(bridge) {
		if err := initializeProvider(ctx, s, p, bridge); err != nil {
			return err
		}
	}
	return nil
}
func initializeProvider(ctx context.Context, s *store.Store, p model.Provider, bridge string) error {
	if _, err := s.Get(ctx, "providers", p.ID); !errors.Is(err, store.ErrNotFound) {
		return nil
	}
	p.Available = provider.Available(p)
	if p.Kind == "claude" {
		_, err := os.Stat(filepath.Join(filepath.Dir(bridge), "node_modules", "@anthropic-ai", "claude-agent-sdk"))
		p.Available = p.Available && err == nil
	}
	return s.Put(ctx, "providers", p.ID, p, 0)
}
func initializeSettings(ctx context.Context, s *store.Store) error {
	if _, err := s.Get(ctx, "settings", "global"); errors.Is(err, store.ErrNotFound) {
		return s.Put(ctx, "settings", "global", map[string]int{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300}, 0)
	}
	return nil
}
