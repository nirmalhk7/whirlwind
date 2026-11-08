package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyInspectionCannotChangeDurableState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	writable, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.Put(ctx, "questions", "pending", map[string]string{"status": "pending"}, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writable.Close() })
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if _, err := reader.Get(ctx, "questions", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := reader.Put(ctx, "questions", "pending", map[string]string{"status": "cancelled"}, 1); err == nil {
		t.Fatal("inspection changed a pending decision")
	}
}

func TestReadOnlyInspectionDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	reader, err := OpenReadOnly(path)
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		t.Error("inspection created missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("inspection left a database: %v", err)
	}
}
