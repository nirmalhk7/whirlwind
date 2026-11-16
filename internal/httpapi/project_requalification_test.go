package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestConfirmChangedProjectPolicyQueuesOneFreshQualification(t *testing.T) {
	repository := t.TempDir()
	if output, err := exec.Command("git", "-C", repository, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, output)
	}
	for _, args := range [][]string{{"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v %s", err, output)
		}
	}
	projectPolicy := model.Policy{Checks: []model.Check{{Name: "build", Command: []string{"/usr/bin/true"}}}, AllowedPaths: []string{"**"}, RiskRules: []model.RiskRule{{Level: "low", Paths: []string{"**"}}}, Sandbox: &model.SandboxPolicy{AllowedDomains: []string{"semgrep.dev"}}}
	encoded, err := yaml.Marshal(projectPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm changed project policy"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v %s", err, output)
		}
	}
	server := setup(t)
	ctx := context.Background()
	identity := &model.QualityIdentity{Version: 1, QltyVersion: "old-qualified-runtime"}
	project := model.Project{ID: "policy-change", Name: "Policy", Path: repository, Status: "approved", PolicyHash: "old-approved-hash", QualityIdentity: identity}
	if err := server.Store.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.Put(ctx, "operations", "prior-setup", model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "complete", Input: map[string]any{"onboarding": true}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.Put(ctx, "operations", "initial-ideation", model.Operation{Kind: "ideation", Subject: project.ID, Stage: "queued"}, 0); err != nil {
		t.Fatal(err)
	}
	confirm := func(key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://localhost/api/v1/projects/"+project.ID+"/policy", nil)
		request.Header.Set(ifMatchHeader, "1")
		request.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	response := confirm("confirm-policy")
	if response.Code != 200 {
		t.Fatalf("confirm changed policy: %d %s", response.Code, response.Body)
	}
	record, err := server.Store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Decode[model.Project](record)
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != "draft" || project.QualityIdentity != nil || project.PolicyHash == "old-approved-hash" {
		t.Fatalf("changed confirmed policy kept stale qualification ready: %+v", project)
	}
	operations, err := server.Store.List(ctx, "operations")
	if err != nil {
		t.Fatal(err)
	}
	setupCount, ideationCount, queuedCount := 0, 0, 0
	for _, item := range operations {
		operation, decodeErr := store.Decode[model.Operation](item)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if operation.Subject != project.ID {
			continue
		}
		if operation.Kind == "project_setup" {
			setupCount++
			if operation.Stage == "queued" {
				queuedCount++
				var input struct {
					Onboarding bool `json:"onboarding"`
				}
				encodedInput, _ := json.Marshal(operation.Input)
				if err := json.Unmarshal(encodedInput, &input); err != nil || !input.Onboarding {
					t.Fatalf("requalification lost onboarding mode: %+v", operation.Input)
				}
			}
		}
		if operation.Kind == "ideation" {
			ideationCount++
		}
	}
	if setupCount != 2 || queuedCount != 1 || ideationCount != 1 {
		t.Fatalf("policy confirmation duplicated onboarding work: setup=%d queued=%d ideation=%d", setupCount, queuedCount, ideationCount)
	}
	if duplicate := confirm("confirm-policy"); duplicate.Code != 200 {
		t.Fatalf("idempotent confirmation changed receipt: %d %s", duplicate.Code, duplicate.Body)
	}
	if stale := confirm("confirm-policy-again"); stale.Code != 409 {
		t.Fatalf("stale repeat confirmation accepted: %d %s", stale.Code, stale.Body)
	}
}
