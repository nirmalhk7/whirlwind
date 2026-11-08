package main

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

type statusExecutor struct{ api *httpapi.Server }

func (e statusExecutor) Execute(ctx context.Context, request control.Request) (any, error) {
	return e.api.ExecuteLocal(ctx, httpapi.LocalCommand{Operation: request.Operation})
}

func TestOnlineDashboardLoadsAgentQueuesAndConnectionResults(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Put(ctx, "providers", "offline", model.Provider{ID: "offline", Kind: "unavailable"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "agents", "code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "offline", Model: "configured-model"}}}, 0); err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(db, workflow.New(db, nil, data), "")
	server, err := control.Listen(control.SocketPath(data), control.SupervisorDispatcher{DB: db.DB, Commands: statusExecutor{api}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	snapshot, err := loadSnapshot(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot["agents"]) != 1 {
		t.Fatal("online dashboard omitted configured agent queue")
	}
	if len(snapshot["provider_status"]) != 1 {
		t.Fatal("online dashboard omitted connection results")
	}
	status, err := store.Decode[workflow.ProviderStatus](snapshot["provider_status"][0])
	if err != nil || status.ID != "offline" || status.Status != "unavailable" {
		t.Fatalf("connection result lost: %+v %v", status, err)
	}
}
