package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

const (
	requalificationFixtureObsoleteSetup = "obsolete-setup"
	requalificationFixtureReceipt       = "confirm-policy"
)

func TestConfirmChangedProjectPolicyQueuesOneFreshQualification(t *testing.T) {
	for _, active := range []string{"", "queued", "running"} {
		name := "no active setup"
		if active != "" {
			name = active + " setup"
		}
		t.Run(name, func(t *testing.T) {
			f := newPolicyConfirmationFixture(t, true)
			prior := model.Operation{Kind: "project_setup", Subject: f.project.ID, Stage: "complete", Input: map[string]any{"onboarding": true}}
			f.putOperation(t, "prior-setup", prior)
			if active != "" {
				obsolete := model.Operation{Kind: "project_setup", Subject: f.project.ID, Stage: active, ConfigurationHash: "old-config", Input: map[string]any{"onboarding": true, "phase": "qualify"}}
				f.putOperation(t, requalificationFixtureObsoleteSetup, obsolete)
			}
			ideation := model.Operation{Kind: "ideation", Subject: f.project.ID, Stage: "queued"}
			f.putOperation(t, "initial-ideation", ideation)

			response := f.confirm(requalificationFixtureReceipt)
			if response.Code != http.StatusOK {
				t.Fatalf("confirm changed policy: %d %s", response.Code, response.Body)
			}
			if duplicate := f.confirm(requalificationFixtureReceipt); duplicate.Code != http.StatusOK || duplicate.Body.String() != response.Body.String() {
				t.Fatalf("idempotent confirmation changed receipt: %d %s", duplicate.Code, duplicate.Body)
			}
			if stale := f.confirm("confirm-policy-again"); stale.Code != http.StatusConflict {
				t.Fatalf("stale repeat confirmation accepted: %d %s", stale.Code, stale.Body)
			}
			current := f.savedProject(t)
			if current.Status != "draft" || current.QualityIdentity != nil || current.PolicyHash != f.committedHash {
				t.Fatalf("changed confirmed policy kept stale qualification ready: %+v", current)
			}

			operations := f.operations(t)
			expectedCount := 3
			if active != "" {
				expectedCount++
			}
			if len(operations) != expectedCount || operations["prior-setup"].Stage != "complete" || operations["initial-ideation"].Stage != "queued" {
				t.Fatalf("confirmation modified history or duplicated ideation: %+v", operations)
			}
			if active != "" {
				obsolete := operations[requalificationFixtureObsoleteSetup]
				if obsolete.Stage != "cancelled" || obsolete.FinishedAt == "" || obsolete.Error == "" || obsolete.ConfigurationHash != "old-config" {
					t.Fatalf("superseded setup lost its history or remained eligible: %+v", obsolete)
				}
				// A worker holding the previous version must not publish stale evidence.
				obsolete.Stage = "complete"
				if err := f.server.Store.Put(t.Context(), "operations", requalificationFixtureObsoleteSetup, obsolete, 1); !errors.Is(err, store.ErrConflict) {
					t.Fatalf("old setup worker could publish over superseded operation: %v", err)
				}
			}

			var queued int
			for _, operation := range operations {
				if operation.Kind != "project_setup" || operation.Subject != f.project.ID || operation.Stage != "queued" {
					continue
				}
				queued++
				input := encodedInput(operation.Input)
				if string(input) != `{"onboarding":true}` {
					t.Fatalf("fresh setup did not start at onboarding: %s", input)
				}
			}
			if queued != 1 {
				t.Fatalf("queued setup count=%d, want one", queued)
			}
		})
	}
}

func TestConfirmUnchangedProjectPolicyKeepsQualification(t *testing.T) {
	f := newPolicyConfirmationFixture(t, false)
	if response := f.confirm(requalificationFixtureReceipt); response.Code != http.StatusOK {
		t.Fatalf("confirm unchanged policy: %d %s", response.Code, response.Body)
	}
	project := f.savedProject(t)
	if project.Status != "approved" || project.QualityIdentity == nil || project.QualityIdentity.QltyVersion != f.project.QualityIdentity.QltyVersion || len(f.operations(t)) != 0 {
		t.Fatalf("unchanged policy lost qualification or queued work: %+v", project)
	}
}

func TestConfirmChangedProjectPolicyRollsBackWithoutReceipt(t *testing.T) {
	f := newPolicyConfirmationFixture(t, true)
	if _, err := f.server.Store.DB.Exec("CREATE TRIGGER fail_policy_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if response := f.confirm(requalificationFixtureReceipt); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed persistence reported success: %d %s", response.Code, response.Body)
	}
	current := f.savedProject(t)
	if current.Status != "approved" || current.PolicyHash != f.project.PolicyHash || current.QualityIdentity == nil || len(f.operations(t)) != 0 {
		t.Fatalf("failed confirmation persisted only part of the mutation: %+v", current)
	}
	if _, err := f.server.Store.DB.Exec("DROP TRIGGER fail_policy_receipt"); err != nil {
		t.Fatal(err)
	}
	if response := f.confirm(requalificationFixtureReceipt); response.Code != http.StatusOK {
		t.Fatalf("after persistence recovery: %d %s", response.Code, response.Body)
	}
	if f.savedProject(t).Status != "draft" || len(f.operations(t)) != 1 {
		t.Fatal("retry did not commit the whole mutation")
	}
}

type policyConfirmationFixture struct {
	server        *Server
	project       model.Project
	committedHash string
}

func newPolicyConfirmationFixture(t *testing.T, changed bool) policyConfirmationFixture {
	t.Helper()
	repository := t.TempDir()
	mustCommand(t, repository, "git", "init", "--quiet")
	mustCommand(t, repository, "git", "config", "user.name", "Test")
	mustCommand(t, repository, "git", "config", "user.email", "test@example.com")
	projectPolicy := model.Policy{
		Checks:       []model.Check{{Name: "build", Command: []string{"/usr/bin/true"}}},
		AllowedPaths: []string{"**"},
		RiskRules:    []model.RiskRule{{Level: "low", Paths: []string{"**"}}},
	}
	encoded, err := yaml.Marshal(projectPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, repository, "git", "add", ".whirlwind.yaml")
	mustCommand(t, repository, "git", "-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project policy")
	_, committedHash, err := policy.Load(repository)
	if err != nil {
		t.Fatal(err)
	}
	server := setup(t)
	oldHash := committedHash
	if changed {
		oldHash = "old-approved-hash"
	}
	project := model.Project{
		ID: "policy-change", Name: "Policy", Path: repository, Status: "approved", PolicyHash: oldHash,
		QualityIdentity: &model.QualityIdentity{Version: 1, QltyVersion: "old-qualified-runtime"},
	}
	if err := server.Store.Put(t.Context(), "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	return policyConfirmationFixture{server: server, project: project, committedHash: committedHash}
}

func (f policyConfirmationFixture) confirm(key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://localhost/api/v1/projects/"+f.project.ID+"/policy", nil)
	request.Header.Set(ifMatchHeader, "1")
	request.Header.Set(idempotencyKeyHeader, key)
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	return response
}

func (f policyConfirmationFixture) savedProject(t *testing.T) model.Project {
	t.Helper()
	record, err := f.server.Store.Get(t.Context(), "projects", f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func (f policyConfirmationFixture) putOperation(t *testing.T, id string, operation model.Operation) {
	t.Helper()
	if err := f.server.Store.Put(t.Context(), "operations", id, operation, 0); err != nil {
		t.Fatal(err)
	}
}

func (f policyConfirmationFixture) operations(t *testing.T) map[string]model.Operation {
	t.Helper()
	records, err := f.server.Store.List(t.Context(), "operations")
	if err != nil {
		t.Fatal(err)
	}
	operations := make(map[string]model.Operation, len(records))
	for _, record := range records {
		operation, err := store.Decode[model.Operation](record)
		if err != nil {
			t.Fatal(err)
		}
		operations[record.ID] = operation
	}
	return operations
}
