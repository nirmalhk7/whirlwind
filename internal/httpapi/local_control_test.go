package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const stagedProjectFile = "kept.txt"

func TestLocalDirectionApprovalReusesVersionedDomainMutation(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	if err := server.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.Put(ctx, "directions", "d", model.Direction{ID: "d", ProjectID: "p", Title: "Keep work safe"}, 0); err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "direction.approve", ResourceID: "d", ExpectedVersion: 1, IdempotencyKey: "decision-1", Payload: json.RawMessage(`{}`)}
	first, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("idempotent retry changed receipt: %s / %s", first, second)
	}
	record, err := server.Store.Get(ctx, "directions", "d")
	if err != nil {
		t.Fatal(err)
	}
	direction, err := store.Decode[model.Direction](record)
	if err != nil || !direction.Selected || direction.Status != "selected" {
		t.Fatalf("direction was not approved through domain handler: %+v (%v)", direction, err)
	}
	if _, err := server.ExecuteLocal(ctx, LocalCommand{Operation: "raw.http", ResourceID: "d", ExpectedVersion: 1, IdempotencyKey: "bad"}); err == nil {
		t.Fatal("arbitrary HTTP operation was accepted")
	}
}

func TestLocalDirectionReconsiderationRequiresExactHumanScopeAndReceipt(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	if err := server.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	direction := model.Direction{ID: "dismissed", ProjectID: "p", Day: "2026-11-06", Status: "dismissed", Category: "harden", Title: "Keep offline drafts", Problem: "Expired sessions lose offline drafts", Mechanism: "Refresh the authorized session before retrying", Components: []string{"internal/auth"}, RepositoryRevision: "rev1", Evidence: []model.EvidenceRef{{Kind: "code", Ref: "internal/auth/session.go", Revision: "rev1"}}, RejectionReason: "Original rejection is retained"}
	if err := server.Store.Put(ctx, "directions", direction.ID, direction, 0); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"project_id":"p","reason":"The operator changed the goal to preserve offline drafts."}`)
	command := LocalCommand{Operation: "direction.reconsider", ProjectID: "p", ResourceID: direction.ID, ExpectedVersion: 1, IdempotencyKey: "reconsider-1", Payload: payload}
	first, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("human reconsideration retry changed its receipt: %s / %s (%v)", first, second, err)
	}
	savedRecord, err := server.Store.Get(ctx, "directions", direction.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Direction](savedRecord)
	if err != nil || saved.Status != "reconsider_requested" || saved.Selected || saved.RejectionReason != direction.RejectionReason {
		t.Fatalf("reconsideration changed original rejection or started work: %+v (%v)", saved, err)
	}
	requests, err := server.Store.List(ctx, "proposal_reconsiderations")
	if err != nil || len(requests) != 1 {
		t.Fatalf("host reconsideration receipt not persisted exactly once: %d (%v)", len(requests), err)
	}
	var request map[string]any
	if err := json.Unmarshal(requests[0].Data, &request); err != nil {
		t.Fatal(err)
	}
	if request["project_id"] != "p" || request["predecessor_id"] != direction.ID || request["reason"] != "The operator changed the goal to preserve offline drafts." || request["status"] != "pending" {
		t.Fatalf("human request was not bound to the exact project, predecessor, and reason: %#v", request)
	}
	revisit := direction
	revisit.ID, revisit.Day, revisit.Title, revisit.Status, revisit.PredecessorID, revisit.RevisitReason = "revisit", "2026-11-07", "Preserve offline drafts during recovery", "proposed", direction.ID, "The operator changed the goal to preserve offline drafts."
	if err := server.Store.SaveDirections(ctx, "p", revisit.Day, []model.Direction{revisit}); err != nil {
		t.Fatalf("human command did not allow one labeled proposal revisit: %v", err)
	}
	duplicate := revisit
	duplicate.ID, duplicate.Title = "revisit-duplicate", "Try reconsideration again"
	if err := server.Store.SaveDirections(ctx, "p", duplicate.Day, []model.Direction{duplicate}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("consumed human request authorized another owner: %v", err)
	}
	if tasks, err := server.Store.List(ctx, "tasks"); err != nil || len(tasks) != 0 {
		t.Fatalf("reconsideration selected a direction or started a plan: tasks=%d err=%v", len(tasks), err)
	}
	stale := command
	stale.ExpectedVersion, stale.IdempotencyKey = 1, "reconsider-stale"
	if _, err := server.ExecuteLocal(ctx, stale); err == nil {
		t.Fatal("stale reconsideration request was accepted")
	}
	crossProject := command
	crossProject.ProjectID, crossProject.ExpectedVersion, crossProject.IdempotencyKey = "other", 2, "reconsider-cross-project"
	crossProject.Payload = json.RawMessage(`{"project_id":"other","reason":"Wrong project"}`)
	if _, err := server.ExecuteLocal(ctx, crossProject); err == nil {
		t.Fatal("cross-project reconsideration request was accepted")
	}
}

func TestLocalFinalizationApprovalRequiresReviewedIdentityAndReceiptKey(t *testing.T) {
	server := setup(t)
	_, err := server.ExecuteLocal(context.Background(), LocalCommand{Operation: "finalization.approve", PlanID: "task", ExpectedVersion: 1, IdempotencyKey: "approval-1", Payload: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "exact approved plan, candidate, review, verification, and policy identities") {
		t.Fatalf("approval without exact reviewed identity was accepted or misreported: %v", err)
	}
	_, err = server.ExecuteLocal(context.Background(), LocalCommand{Operation: "finalization.approve", PlanID: "task", ExpectedVersion: 1, Payload: json.RawMessage(`{"plan_version":2,"approved_plan_hash":"plan-hash","reviewed_commit":"commit","review_id":"review","verification_id":"verification","policy_hash":"policy"}`)})
	if err == nil || !strings.Contains(err.Error(), "expected task version and idempotency key") {
		t.Fatalf("approval without an idempotency key was accepted or misreported: %v", err)
	}
}

func TestLocalProjectRegistrationNeedsNoRemoteAndPreservesStaging(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	repository := t.TempDir()
	mustCommand(t, repository, "git", "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repository, stagedProjectFile), []byte("local source"), 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, repository, "git", "add", stagedProjectFile)
	before := mustCommand(t, repository, "git", "status", "--porcelain")
	payload, _ := json.Marshal(map[string]string{"name": "Local project", "path": repository})
	response, err := server.ExecuteLocal(ctx, LocalCommand{Operation: "project.register", IdempotencyKey: "register-local", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var saved model.Record
	if err := json.Unmarshal(response, &saved); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](saved)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	if project.Path != canonical || project.Repo != canonical {
		t.Fatalf("local repository identity changed: %+v", project)
	}
	after := mustCommand(t, repository, "git", "status", "--porcelain")
	if before != after {
		t.Fatalf("registration changed staging state: before %q after %q", before, after)
	}
	content, err := os.ReadFile(filepath.Join(repository, stagedProjectFile))
	if err != nil || string(content) != "local source" {
		t.Fatalf("registration changed repository file: %q (%v)", content, err)
	}
}

func mustCommand(t *testing.T, directory string, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
	return string(output)
}

func TestLocalDirectionRejectionRecordsHumanReason(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	if err := server.Store.Put(ctx, "directions", "proposal", model.Direction{ID: "proposal", ProjectID: "p", Status: "proposed"}, 0); err != nil {
		t.Fatal(err)
	}
	_, err := server.ExecuteLocal(ctx, LocalCommand{Operation: "direction.reject", ResourceID: "proposal", ExpectedVersion: 1, IdempotencyKey: "vim-reject", Payload: json.RawMessage(`{"reason":"Already covered.\nKeep existing tests."}`)})
	if err != nil {
		t.Fatal(err)
	}
	record, err := server.Store.Get(ctx, "directions", "proposal")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Direction](record)
	if err != nil || saved.Status != "dismissed" || saved.RejectionReason != "Already covered.\nKeep existing tests." {
		t.Fatalf("decision reason not recorded: %+v (%v)", saved, err)
	}
}
