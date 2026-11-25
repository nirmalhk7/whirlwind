package workflow

import (
	"context"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	outcomePartialRunID = "partial-run"
)

func TestParityQueuedIdeationPreservesPartialOutcome(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	// Persist one accepted direction before interrupted generation is resumed.
	op := model.Operation{Kind: "ideation", Subject: f.project.ID, Stage: "queued", Input: map[string]string{"day": "2026-11-11"}}
	requireWorkflowSuccess(t, f.engine.Store.Put(ctx, "operations", outcomePartialRunID, op, 0))
	id, _, err := f.engine.Store.ReserveIdeaCandidate(ctx, outcomePartialRunID, "expand")
	requireWorkflowSuccess(t, err)
	idea := model.Idea{Title: "Invoice export", ID: id, ProjectID: f.project.ID, RunID: outcomePartialRunID, Direction: "expand", What: "Export invoices", Why: "Keep accounting accurate", Where: "billing", HowItHelps: "Review totals", DecisionStatus: "draft", NoveltyMethod: "legacy_llm"}
	requireWorkflowSuccess(t, f.engine.Store.CompleteIdeaCandidate(ctx, id, &idea, ""))
	// Exhaust only remaining directions, making completion deterministically partial.
	for _, direction := range []string{"optimize", "harden", "simplify"} {
		for n := 0; n < 5; n++ {
			candidate, _, err := f.engine.Store.ReserveIdeaCandidate(ctx, outcomePartialRunID, direction)
			requireWorkflowSuccess(t, err)
			requireWorkflowSuccess(t, f.engine.Store.CompleteIdeaCandidate(ctx, candidate, nil, "similar candidate"))
		}
	}
	record, _ := f.engine.Store.Get(ctx, "operations", outcomePartialRunID)
	lease, err := f.engine.Store.Claim(ctx, "operation:partial-run", f.engine.owner, 60000000000)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, f.engine.runQueuedIdeation(ctx, f.project, record, "operation:partial-run", lease.Fence))
	saved, err := f.engine.Store.Get(ctx, "operations", outcomePartialRunID)
	requireWorkflowSuccess(t, err)
	result, _ := store.Decode[model.Operation](saved)
	if result.Stage != "partial" {
		t.Fatalf("accepted direction lost in aggregate outcome: %+v", result)
	}
	var count int
	if err := f.engine.Store.DB.QueryRow("SELECT count(*) FROM ideation_directions WHERE run_id='partial-run' AND status='error' AND error_reason<>''").Scan(&count); err != nil || count != 3 {
		t.Fatalf("failed directions lack durable reasons: %d %v", count, err)
	}
}

func TestParityExhaustedDirectionDoesNotConsumeOtherDirectionAllowances(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	if err := e.Store.Put(ctx, "operations", "run", model.Operation{Kind: "ideation", Subject: "p", Stage: "running"}, 0); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		id, _, err := e.Store.ReserveIdeaCandidate(ctx, "run", "expand")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.CompleteIdeaCandidate(ctx, id, nil, "similar"); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := e.reserveDirectionCandidates(ctx, "run", nil)
	if err != nil || len(ids) != 3 || ids["expand"] != "" {
		t.Fatalf("one exhausted direction blocked independent candidates: %+v %v", ids, err)
	}
	var count int
	if err := e.Store.DB.QueryRow("SELECT candidate_count FROM ideation_directions WHERE run_id='run' AND direction='expand'").Scan(&count); err != nil || count != 5 {
		t.Fatalf("exhausted direction got extra allowance: %d %v", count, err)
	}
}

func TestParityIdeationIgnoresModelCandidatesWithoutRemainingAllowance(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	if err := f.engine.Store.Put(ctx, "operations", "run", model.Operation{Kind: "ideation", Subject: f.project.ID, Stage: "running"}, 0); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		id, _, err := f.engine.Store.ReserveIdeaCandidate(ctx, "run", "expand")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.engine.Store.CompleteIdeaCandidate(ctx, id, nil, "similar"); err != nil {
			t.Fatal(err)
		}
	}
	err := f.engine.Ideate(WithOperation(ctx, "run"), f.project, "2026-11-11")
	if err == nil {
		t.Fatal("model bypassed exhausted direction allowance")
	}
	ideas, err := f.engine.Store.List(ctx, "ideas")
	if err != nil || len(ideas) != 3 {
		t.Fatalf("remaining independent directions lost: %d %v", len(ideas), err)
	}
	for _, record := range ideas {
		idea, err := store.Decode[model.Idea](record)
		if err != nil || idea.Direction == "expand" {
			t.Fatalf("unbudgeted model candidate persisted: %+v %v", idea, err)
		}
	}
}
