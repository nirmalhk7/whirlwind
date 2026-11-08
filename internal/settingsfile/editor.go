package settingsfile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type FileView struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	LoadedAt string `json:"loaded_at,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// Snapshot returns the exact bytes accepted at startup, independent of later disk edits.
func (s *Source) Snapshot() FileView { return s.startup }

// File reads the configured startup path, even if its current contents are invalid YAML.
func (s *Source) File() (FileView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return FileView{}, err
	}
	return FileView{Path: s.path, Content: string(raw), Revision: revision(raw)}, nil
}

func revision(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func ValidateFile(content string) error { _, err := decode([]byte(content)); return err }

// SaveFile preserves submitted YAML bytes and commits runtime changes and the receipt
// together. Failed persistence restores the prior file; stale editors cannot overwrite it.
func (s *Source) SaveFile(ctx context.Context, content, expected string, commit func([]store.Change) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, previous, changes, err := s.prepareSave(ctx, content, expected)
	if err != nil {
		return err
	}
	next := []byte(content)
	if err := atomicWrite(s.path, next); err != nil {
		return err
	}
	if err := s.commitDocument(func() error { return commit(changes) }, next, previous); err != nil {
		return err
	}
	s.raw = append([]byte{}, next...)
	s.doc = doc
	return nil
}

func (s *Source) prepareSave(ctx context.Context, content, expected string) (Document, []byte, []store.Change, error) {
	doc, err := decode([]byte(content))
	if err != nil {
		return Document{}, nil, nil, err
	}
	previous, err := os.ReadFile(s.path)
	if err != nil {
		return Document{}, nil, nil, err
	}
	if expected == "" || revision(previous) != expected {
		return Document{}, nil, nil, store.ErrConflict
	}
	changes, err := s.prepareSaveChanges(ctx, previous, doc)
	if err != nil {
		return Document{}, nil, nil, err
	}
	return doc, previous, changes, nil
}

func (s *Source) prepareSaveChanges(ctx context.Context, previous []byte, doc Document) ([]store.Change, error) {
	prior, priorErr := decode(previous)
	priorMode := s.activeMode
	if priorErr == nil {
		priorMode = prior.Mode
	}
	if model.EffectiveMode(priorMode) != model.EffectiveMode(doc.Mode) {
		return nil, errors.New("mode is editable only on disk; restart Whirlwind to activate it")
	}
	return s.configurationChanges(store.Unstaged(store.WithoutSettingsSource(ctx)), doc)
}

func (s *Source) ValidateModeEdit(content string) error {
	doc, err := decode([]byte(content))
	if err != nil {
		return err
	}
	file, err := s.File()
	if err != nil {
		return err
	}
	prior, parseErr := decode([]byte(file.Content))
	mode := s.activeMode
	if parseErr == nil {
		mode = prior.Mode
	}
	if model.EffectiveMode(mode) != model.EffectiveMode(doc.Mode) {
		return errors.New("mode is editable only on disk; restart Whirlwind to activate it")
	}
	return nil
}
