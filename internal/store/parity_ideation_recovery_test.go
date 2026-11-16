package store

import (
	"context"
	"testing"
	"whirlwind/internal/model"
)

func TestParityInterruptedIdeationRequeuesSameRunAndCandidateAllowance(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	op := model.Operation{Kind: "ideation", Subject: "p", Stage: "running", Owner: "previous", Input: map[string]string{"day": "2026-11-11"}}
	if err := s.Put(ctx, "operations", "run", op, 0); err != nil {
		t.Fatal(err)
	}
	id, ordinal, err := s.ReserveIdeaCandidate(ctx, "run", "expand")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx, "replacement"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, "operations", "run")
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := Decode[model.Operation](r)
	if saved.Stage != "queued" || saved.Subject != "p" {
		t.Fatalf("interrupted durable run cannot resume: %+v", saved)
	}
	next, nextOrdinal, err := s.ReserveIdeaCandidate(ctx, "run", "expand")
	if err != nil || next != id || nextOrdinal != ordinal {
		t.Fatalf("restart manufactured candidate allowance: %s/%d -> %s/%d %v", id, ordinal, next, nextOrdinal, err)
	}
}

func TestParityInterruptedIdeationPreservesHumanWaitInsteadOfRequeue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "operations", "run", model.Operation{Kind: "ideation", Subject: "p", Stage: "running", Owner: "previous"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "sessions", "attempt", model.Attempt{AttemptID: "attempt", Task: "ideation:p:run", OperationID: "run", ProjectID: "p", Role: "ideate", Owner: "previous", Finished: false, Status: "running"}, 0); err != nil {
		t.Fatal(err)
	}
	question := model.QuestionSet{ID: "question", TaskID: "ideation:p:run", AttemptID: "attempt", Status: "pending", Questions: []model.Question{{ID: "q", Text: "Which direction?"}}}
	if err := s.Put(ctx, "questions", question.ID, question, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx, "replacement"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, "operations", "run")
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := Decode[model.Operation](r)
	if saved.Stage != "paused" {
		t.Fatalf("pending human decision was bypassed: %+v", saved)
	}
	q, err := s.Get(ctx, "questions", question.ID)
	if err != nil || q.Version != 1 {
		t.Fatalf("recovery rewrote pending human question: %+v %v", q, err)
	}
}
