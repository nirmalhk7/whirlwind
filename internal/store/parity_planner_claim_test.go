package store

import (
	"context"
	"errors"
	"testing"

	"whirlwind/internal/model"
)

const (
	plannerClaimFixtureAssignedFile = "/assigned/plan.md"
)

func TestParityPlannerAttemptClaimsIdeaAtomically(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["decision_status"] = "approved"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{"id": "plan", "project_id": "p", "idea_id": "i", "ideation_run_id": "source", "stage": "planning"}
	if err := s.Put(ctx, "execution_plans", "plan", plan, 0); err != nil {
		t.Fatal(err)
	}
	attempt := map[string]any{"attempt_id": "a", "plan_id": "plan", "project_id": "p", "idea_id": "i", "operation_id": "source", "role": "plan", "status": "running", "started_at": "now"}
	if err := s.Put(ctx, "sessions", "a", attempt, 0); err != nil {
		t.Fatal(err)
	}
	var state, run string
	if err := s.DB.QueryRow("SELECT status FROM ideas WHERE idea_id='i'").Scan(&state); err != nil || state != "in_process" {
		t.Fatalf("attempt launched without atomic claim: %q %v", state, err)
	}
	if err := s.DB.QueryRow("SELECT planner_run_id FROM planner_runs WHERE idea_id='i' AND status='in_process'").Scan(&run); err != nil || run != "a" {
		t.Fatalf("planner did not reuse ACP attempt ID: %q %v", run, err)
	}
	attempt["attempt_id"] = "duplicate"
	if err := s.Put(ctx, "sessions", "duplicate", attempt, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent claim accepted: %v", err)
	}
	if _, err := s.Get(ctx, "sessions", "duplicate"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed claim leaked attempt: %v", err)
	}
}

func TestParityPlannerCannotClaimArchivedIdea(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["decision_status"] = "approved"
	idea["archived_at"] = "now"
	idea["archive_reason"] = "Human rejected"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err != nil {
		t.Fatal(err)
	}
	attempt := map[string]any{"attempt_id": "a", "plan_id": "plan", "project_id": "p", "idea_id": "i", "operation_id": "source", "role": "plan", "status": "running", "started_at": "now"}
	if err := s.Put(ctx, "sessions", "a", attempt, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived idea launched: %v", err)
	}
}

func TestParityPlannerPublicationLinksAllProvenance(t *testing.T) {
	t.Run("recover_legacy_provider_completion", func(t *testing.T) { checkPlannerPublication(t, false) })
	t.Run("finalize_pending_publication_atomically", func(t *testing.T) { checkPlannerPublication(t, true) })
}

func checkPlannerPublication(t *testing.T, pending bool) {
	t.Helper()
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["decision_status"] = "approved"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{"id": "plan", "project_id": "p", "idea_id": "i", "ideation_run_id": "source", "stage": "planning"}
	if err := s.Put(ctx, "execution_plans", "plan", plan, 0); err != nil {
		t.Fatal(err)
	}
	attempt := map[string]any{"attempt_id": "a", "plan_id": "plan", "project_id": "p", "idea_id": "i", "operation_id": "source", "role": "plan", "status": "running", "started_at": "now"}
	if err := s.Put(ctx, "sessions", "a", attempt, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.PlannerRunLocation(ctx, "a", plannerClaimFixtureAssignedFile); err != nil {
		t.Fatal(err)
	}
	attempt["status"], attempt["finished"], attempt["success"] = "complete", true, true
	if pending {
		attempt["status"], attempt["success"] = model.AttemptAwaitingPlanPublication, nil
	}
	attempt["finished_at"] = "2026-11-11T00:00:00Z"
	if err := s.Put(ctx, "sessions", "a", attempt, 1); err != nil {
		t.Fatal(err)
	}
	plan["stage"], plan["last_attempt_id"] = "plan_approval", "a"
	plan["plan_document"] = map[string]any{"path": plannerClaimFixtureAssignedFile, "hash": "verified-hash"}
	plan["plan"] = map[string]any{"requirements": []map[string]string{{"id": "R1", "outcome": "Export invoices", "evidence_method": "behavior test"}}}
	if pending {
		document := plan["plan_document"].(map[string]any)
		document["path"] = "/different/plan.md"
		if err := s.Put(ctx, "execution_plans", "plan", plan, 1); !errors.Is(err, ErrConflict) {
			t.Fatalf("finalized a mismatched publication path: %v", err)
		}
		record, err := s.Get(ctx, "sessions", "a")
		if err != nil {
			t.Fatal(err)
		}
		current, err := Decode[model.Attempt](record)
		if err != nil || current.Status != model.AttemptAwaitingPlanPublication || current.Success != nil {
			t.Fatalf("failed publication did not roll back the attempt transition: %+v %v", current, err)
		}
		document["path"] = plannerClaimFixtureAssignedFile
	}
	if err := s.Put(ctx, "execution_plans", "plan", plan, 1); err != nil {
		t.Fatal(err)
	}
	var status, linked string
	if err := s.DB.QueryRow("SELECT i.status,json_extract(r.data,'$.plan_id') FROM ideas i JOIN resources r ON r.kind='ideas' AND r.id=i.idea_id WHERE i.idea_id='i'").Scan(&status, &linked); err != nil || status != "complete" || linked != "plan" {
		t.Fatalf("publication did not atomically complete idea: %s %s %v", status, linked, err)
	}
	if err := s.DB.QueryRow("SELECT status,plan_id FROM planner_runs WHERE planner_run_id='a'").Scan(&status, &linked); err != nil || status != "completed" || linked != "plan" {
		t.Fatalf("publication did not complete ACP run: %s %s %v", status, linked, err)
	}
	record, err := s.Get(ctx, "sessions", "a")
	if err != nil {
		t.Fatal(err)
	}
	current, err := Decode[model.Attempt](record)
	if err != nil || current.Status != "complete" || current.Success == nil || !*current.Success {
		t.Fatalf("publication did not complete the attempt: %+v %v", current, err)
	}
}
