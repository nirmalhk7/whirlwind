package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"whirlwind/internal/model"
)

const (
	queuedCandidateInsertSQL = "INSERT INTO ideation_directions(run_id,direction,status) VALUES('source','expand','queued')"
)

func TestParityCandidateAllowanceSurvivesInterruptedGeneration(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	parityIdea(t, s)
	if _, err := s.DB.Exec(queuedCandidateInsertSQL); err != nil {
		t.Fatal(err)
	}
	registry, ok := any(s).(interface {
		ReserveIdeaCandidate(context.Context, string, string) (string, int, error)
	})
	if !ok {
		t.Fatal("candidate allowances are not durably reserved before generation")
	}
	for ordinal := 1; ordinal <= 5; ordinal++ {
		id, actual, err := registry.ReserveIdeaCandidate(ctx, "source", "expand")
		if err != nil || actual != ordinal {
			t.Fatalf("reservation %d: %s %d %v", ordinal, id, actual, err)
		}
		resumed, resumedOrdinal, err := registry.ReserveIdeaCandidate(ctx, "source", "expand")
		if err != nil || resumed != id || resumedOrdinal != ordinal {
			t.Fatalf("restart manufactured new allowance: %s %d %v", resumed, resumedOrdinal, err)
		}
		if _, err := s.DB.Exec("UPDATE idea_candidates SET status='rejected' WHERE candidate_id=?", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := registry.ReserveIdeaCandidate(ctx, "source", "expand"); !errors.Is(err, ErrConflict) {
		t.Fatalf("sixth candidate allowed: %v", err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT candidate_count FROM ideation_directions WHERE run_id='source' AND direction='expand'").Scan(&count); err != nil || count != 5 {
		t.Fatalf("allowance counter lost: %d %v", count, err)
	}
}

func TestParityCandidatePublicationCommitsIdeaAndDirectionTogether(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	content := parityIdea(t, s)
	if _, err := s.DB.Exec(queuedCandidateInsertSQL); err != nil {
		t.Fatal(err)
	}
	registry, ok := any(s).(interface {
		CompleteIdeaCandidate(context.Context, string, *model.Idea, string) error
	})
	if !ok {
		t.Fatal("accepted directions cannot be published independently and atomically")
	}
	id, _, err := s.ReserveIdeaCandidate(ctx, "source", "expand")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(content)
	var idea model.Idea
	if err := json.Unmarshal(raw, &idea); err != nil {
		t.Fatal(err)
	}
	idea.ID = id
	if err := registry.CompleteIdeaCandidate(ctx, id, &idea, ""); err != nil {
		t.Fatal(err)
	}
	var status, linked string
	if err := s.DB.QueryRow("SELECT status,idea_id FROM ideation_directions WHERE run_id='source' AND direction='expand'").Scan(&status, &linked); err != nil || status != "completed" || linked != id {
		t.Fatalf("direction/idea handoff missing: %s %s %v", status, linked, err)
	}
	if _, err := s.Get(ctx, "ideas", id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveIdeaCandidate(ctx, "source", "expand"); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted direction regenerated: %v", err)
	}
}

func TestParityIdeationRunCannotMoveBetweenProjects(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	op := model.Operation{Kind: "ideation", Subject: "p", Stage: "running", StartedAt: "now"}
	if err := s.Put(ctx, "operations", "run", op, 0); err != nil {
		t.Fatal(err)
	}
	op.Subject = "another-project"
	if err := s.Put(ctx, "operations", "run", op, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("source run changed project: %v", err)
	}
}

func TestParityPausedIdeationCannotPublishOrRecordFalseDuplicate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	parityIdea(t, s)
	if _, err := s.DB.Exec(queuedCandidateInsertSQL); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.ReserveIdeaCandidate(ctx, "source", "expand")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE ideation_runs SET status='paused' WHERE run_id='source'"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIdeaCandidate(ctx, id, nil, "similar"); !errors.Is(err, ErrConflict) {
		t.Fatalf("paused generation manufactured duplicate verdict: %v", err)
	}
	var status string
	if err := s.DB.QueryRow("SELECT status FROM idea_candidates WHERE candidate_id=?", id).Scan(&status); err != nil || status != "generating" {
		t.Fatalf("paused result changed candidate history: %s %v", status, err)
	}
}
