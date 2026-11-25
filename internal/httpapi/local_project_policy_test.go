package httpapi

import (
	"encoding/json"
	"errors"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

func TestManagedPolicyApprovalAtomicallySchedulesQualification(t *testing.T) {
	server := setup(t)
	ctx := t.Context()
	project := model.Project{ID: "managed", Path: t.TempDir(), Status: "approved", PolicyHash: "old", QualityIdentity: &model.QualityIdentity{Version: 1}}
	if err := server.Store.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	old := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "running"}
	if err := server.Store.Put(ctx, "operations", "old-setup", old, 0); err != nil {
		t.Fatal(err)
	}
	const document = "allowed_paths: [result.txt]\nrisk_rules: [{level: low, paths: [result.txt]}]\nchecks: [{name: result, command: [test, -f, result.txt]}]\n"
	payload, err := json.Marshal(map[string]string{"content": document})
	if err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "project.policy.preview", ProjectID: project.ID, Payload: payload}
	body, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	var preview localPolicyPreview
	if err := json.Unmarshal(body, &preview); err != nil || preview.Version != 1 || preview.Hash == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := server.Store.Get(ctx, policy.ManagedPolicyResource, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("preview persisted execution policy: %v", err)
	}

	command.Operation, command.IdempotencyKey = "project.policy.approve", "policy-once"
	command.ExpectedVersion, command.ContentHash = preview.Version, preview.Hash
	if _, err := server.Store.DB.Exec("CREATE TRIGGER block_policy_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ExecuteLocal(ctx, command); err == nil {
		t.Fatal("approval succeeded without a durable receipt")
	}
	if _, err := server.Store.Get(ctx, policy.ManagedPolicyResource, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed commit persisted approval: %v", err)
	}
	if _, err := server.Store.DB.Exec("DROP TRIGGER block_policy_receipt"); err != nil {
		t.Fatal(err)
	}
	approved, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := server.ExecuteLocal(ctx, command); err != nil || string(replay) != string(approved) {
		t.Fatalf("approval replay returned %s, %v", replay, err)
	}
	current, err := server.Engine.Project(ctx, project.ID)
	if err != nil || !current.ManagedPolicy || current.PolicyHash != preview.Hash || current.Status != "draft" || current.QualityIdentity != nil {
		t.Fatalf("saved approval: %+v %v", current, err)
	}
	stored, err := server.Store.Get(ctx, policy.ManagedPolicyResource, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	approvedDocument, err := store.Decode[model.ProjectPolicyDocument](stored)
	if err != nil || approvedDocument.Content != document {
		t.Fatalf("stored policy text changed: %+v %v", approvedDocument, err)
	}
	operations, err := server.Store.List(ctx, "operations")
	if err != nil || len(operations) != 2 {
		t.Fatalf("qualification count: %d, %v", len(operations), err)
	}
	for _, record := range operations {
		operation, err := store.Decode[model.Operation](record)
		want := "queued"
		if record.ID == "old-setup" {
			want = "cancelled"
		}
		if err != nil || operation.Subject != project.ID || operation.Kind != "project_setup" || operation.Stage != want {
			t.Fatalf("qualification transition: %+v, %v", operation, err)
		}
	}
}
