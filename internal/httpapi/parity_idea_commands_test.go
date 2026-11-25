package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityIdeaDecisionsAreIndependentAndArchivalRetainsHistory(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	requireAPISuccess(t, s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0))
	requireAPISuccess(t, s.Store.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete", StartedAt: "now"}, 0))
	for _, id := range []string{"first", "second"} {
		idea := model.Idea{Title: "Invoice export", ID: id, ProjectID: "p", RunID: "source", Direction: "expand", What: id, Why: "Account for every item", Where: "billing", HowItHelps: "Keep reliable totals", DecisionStatus: "draft", NoveltyMethod: "legacy_llm", NoveltyOutcome: "accepted"}
		requireAPISuccess(t, s.Store.Put(ctx, "ideas", id, idea, 0))
		command := LocalCommand{Operation: "idea.approve", ProjectID: "p", ResourceID: id, ExpectedVersion: 1, IdempotencyKey: "approve-" + id}
		first, err := s.ExecuteLocal(ctx, command)
		if err != nil {
			t.Fatalf("idea approval unavailable: %v", err)
		}
		second, err := s.ExecuteLocal(ctx, command)
		if err != nil || string(first) != string(second) {
			t.Fatalf("receipt changed: %v", err)
		}
	}
	record, err := s.Store.Get(ctx, "ideas", "first")
	if err != nil || record.Version != 2 {
		t.Fatalf("peer changed approval: %+v %v", record, err)
	}
	first, err := store.Decode[model.Idea](record)
	if err != nil || first.DecisionStatus != "approved" {
		t.Fatalf("peer deselected: %+v %v", first, err)
	}
	_, err = s.ExecuteLocal(ctx, LocalCommand{Operation: "idea.archive", ProjectID: "p", ResourceID: "first", ExpectedVersion: 2, IdempotencyKey: "archive", Payload: json.RawMessage(`{"reason":"Defer accounting work"}`)})
	requireAPISuccess(t, err)
	record, err = s.Store.Get(ctx, "ideas", "first")
	if err != nil {
		t.Fatal("archival deleted novelty history", err)
	}
	archived, err := store.Decode[model.Idea](record)
	if err != nil || archived.ArchivedAt == "" || archived.DecisionStatus != "approved" || archived.Fingerprint != first.Fingerprint {
		t.Fatalf("archive changed lifecycle or novelty history: %+v %v", archived, err)
	}
}

func TestParityArchivedIdeaReconsiderationPreservesFiveStateHistory(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	requireAPISuccess(t, s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0))
	requireAPISuccess(t, s.Store.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete"}, 0))
	idea := model.Idea{Title: "Invoice export", ID: "idea", ProjectID: "p", RunID: "source", Direction: "expand", What: "Export invoices", Why: "Keep accounting accurate", Where: "billing", HowItHelps: "Review totals", Mechanism: "Export monthly ledger", Components: []string{"billing"}, RepositoryRevision: "old", DecisionStatus: "draft", NoveltyMethod: "legacy_llm", ArchivedAt: "2026-11-11T00:00:00Z", ArchiveReason: "Priorities changed"}
	requireAPISuccess(t, s.Store.Put(ctx, "ideas", idea.ID, idea, 0))
	command := LocalCommand{Operation: "idea.reconsider", ProjectID: "p", ResourceID: idea.ID, ExpectedVersion: 1, IdempotencyKey: "reconsider", Payload: json.RawMessage(`{"reason":"Accounting now requires monthly exports"}`)}
	first, err := s.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatalf("canonical reconsideration unavailable: %v", err)
	}
	replay, err := s.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(replay) {
		t.Fatalf("reconsideration receipt changed: %v", err)
	}
	savedRecord, err := s.Store.Get(ctx, "ideas", idea.ID)
	requireAPISuccess(t, err)
	saved, _ := store.Decode[model.Idea](savedRecord)
	if saved.DecisionStatus != "draft" || saved.ArchivedAt != idea.ArchivedAt || saved.ArchiveReason != idea.ArchiveReason || saved.What != idea.What {
		t.Fatalf("reconsideration rewrote rejection history: %+v", saved)
	}
	requests, err := s.Store.PendingProposalReconsiderations(ctx, "p", 12)
	if err != nil || len(requests) != 1 || requests[0].PredecessorID != idea.ID || requests[0].Reason != "Accounting now requires monthly exports" {
		t.Fatalf("request not available to bounded ideation history: %+v %v", requests, err)
	}
	ideas, err := s.Store.List(ctx, "ideas")
	if err != nil || len(ideas) != 1 {
		t.Fatal("request prematurely bypassed generation/novelty", err)
	}
	requireAPISuccess(t, s.Store.Put(ctx, "operations", "later", model.Operation{Kind: "ideation", Subject: "p", Stage: "running"}, 0))
	id, _, err := s.Store.ReserveIdeaCandidate(ctx, "later", "expand")
	requireAPISuccess(t, err)
	next := idea
	next.ID, next.RunID, next.PredecessorID, next.RevisitReason = id, "later", idea.ID, requests[0].Reason
	next.ArchivedAt, next.ArchiveReason = "", ""
	if err := s.Store.CompleteIdeaCandidate(ctx, id, &next, ""); err != nil {
		t.Fatalf("authorized linked draft could not publish: %v", err)
	}
	nextRecord, err := s.Store.Get(ctx, "ideas", id)
	requireAPISuccess(t, err)
	linked, _ := store.Decode[model.Idea](nextRecord)
	if linked.DecisionStatus != "draft" || linked.PredecessorID != idea.ID || linked.RunID != "later" || linked.ArchivedAt != "" {
		t.Fatalf("reconsideration did not create independent linked draft: %+v", linked)
	}
	if pending, err := s.Store.PendingProposalReconsiderations(ctx, "p", 12); err != nil || len(pending) != 0 {
		t.Fatalf("reconsideration authorization reused: %+v %v", pending, err)
	}
	assertOriginalIdeaUnchanged(t, ctx, s.Store, idea.ID, savedRecord.Data)

}

func assertOriginalIdeaUnchanged(t *testing.T, ctx context.Context, database *store.Store, id string, data []byte) {
	t.Helper()
	original, err := database.Get(ctx, "ideas", id)
	if err != nil || string(original.Data) != string(data) {
		t.Fatalf("linked publication rewrote original: %v", err)
	}
}

func TestPlannerCancellationBeforeIntakeIsVersionedAndIdempotent(t *testing.T) {
	s := setup(t)
	ctx := t.Context()
	requireAPISuccess(t, s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/fixture/project"}, 0))
	requireAPISuccess(t, s.Store.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete"}, 0))
	idea := model.Idea{ID: "idea", ProjectID: "p", RunID: "source", Direction: "expand", Title: "Export", What: "Add an export",
		Why: "Auditing", Where: "billing", HowItHelps: "Review totals", DecisionStatus: "draft", NoveltyMethod: "legacy_llm"}
	requireAPISuccess(t, s.Store.Put(ctx, "ideas", idea.ID, idea, 0))
	approval := LocalCommand{Operation: "idea.approve", ResourceID: idea.ID, ProjectID: "p", ExpectedVersion: 1, IdempotencyKey: "approve-fixture"}
	_, err := s.ExecuteLocal(ctx, approval)
	requireAPISuccess(t, err)
	cancel := LocalCommand{Operation: "planner.cancel", ResourceID: idea.ID, ProjectID: "p", ExpectedVersion: 2, IdempotencyKey: "cancel-fixture"}
	first, err := s.ExecuteLocal(ctx, cancel)
	requireAPISuccess(t, err)
	if replay, err := s.ExecuteLocal(ctx, cancel); err != nil || string(replay) != string(first) {
		t.Fatalf("cancellation receipt changed: %s %v", replay, err)
	}
	record, err := s.Store.Get(ctx, "ideas", idea.ID)
	requireAPISuccess(t, err)
	saved, err := store.Decode[model.Idea](record)
	requireAPISuccess(t, err)
	if record.Version != 3 || saved.DecisionStatus != "error" || saved.ErrorReason != model.PlannerCancellationReason || saved.PlanID != "" {
		t.Fatalf("cancellation was not recorded once: version=%d idea=%+v", record.Version, saved)
	}
}
