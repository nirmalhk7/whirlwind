package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/store"
)

func TestParityExplicitLegacyStateIsRejectedBeforeRecovery(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put(context.Background(), "tasks", "old-work", map[string]string{"stage": "running"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	configuration := config{data: directory}
	opened, lock, err := configuration.openState()
	if opened != nil {
		_ = opened.Close()
	}
	if lock != nil {
		_ = lock.Close()
	}
	if err == nil {
		t.Fatal("explicit data path resumed legacy database without fresh-state cutover")
	}
	if _, err := os.Stat(filepath.Join(directory, "parity-v1.state")); !os.IsNotExist(err) {
		t.Fatalf("legacy directory marked as new state: %v", err)
	}
}
