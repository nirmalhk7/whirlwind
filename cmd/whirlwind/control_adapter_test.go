package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/github"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func TestProjectSetupRetryTraversesSupervisorIPCAndCommandAdapter(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	project := model.Project{ID: "project", Name: "Project", Path: t.TempDir(), Status: "error"}
	if err := database.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	if err := database.Put(ctx, "operations", "failed", model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "failed", Error: "Qlty unavailable"}, 0); err != nil {
		t.Fatal(err)
	}
	engine := workflow.New(database, github.CLI{}, t.TempDir())
	api := httpapi.New(database, engine, "")
	dispatcher := control.SupervisorDispatcher{DB: database.DB, Commands: localCommandExecutor{api: api}}
	controlDirectory, err := os.MkdirTemp("/tmp", "whirlwind-setup-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(controlDirectory) })
	socket := filepath.Join(controlDirectory, "control.sock")
	server, err := control.Listen(socket, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	response, err := control.NewClient(socket).Call(callCtx, control.Request{RequestID: "retry", Operation: "project.setup.retry", ProjectID: project.ID, ExpectedVersion: 1, IdempotencyKey: "retry-1"})
	if err != nil {
		t.Fatalf("setup retry did not traverse supervisor command path: %v", err)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var receipt model.Record
	if err := json.Unmarshal(encoded, &receipt); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Decode[model.Operation](receipt)
	if err != nil || operation.Kind != "project_setup" || operation.Subject != project.ID || operation.Stage != "queued" {
		t.Fatalf("supervisor did not return the durable retry operation: %+v (%v)", operation, err)
	}
}
