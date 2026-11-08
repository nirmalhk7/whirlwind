package httpapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func TestLocalProviderStatusReportsUnavailableConnection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Put(ctx, "providers", "offline", model.Provider{ID: "offline", Kind: "unavailable"}, 0); err != nil {
		t.Fatal(err)
	}
	api := New(db, workflow.New(db, nil, t.TempDir()), "")
	data, err := api.ExecuteLocal(ctx, LocalCommand{Operation: "provider.status"})
	if err != nil {
		t.Fatal(err)
	}
	var status workflow.ProviderStatuses
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Providers) != 1 || status.Providers[0].ID != "offline" || status.Providers[0].Status != "unavailable" {
		t.Fatalf("unavailable connection not reported: %+v", status)
	}
}
