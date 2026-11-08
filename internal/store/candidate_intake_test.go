package store

import (
	"context"
	"errors"
	"testing"
	"whirlwind/internal/model"
)

// Ready label explicitly authorizes Personal workflow intake.
func TestReadyIssueAuthorizesAndDeduplicates(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: "owner/repo"}
	issue := model.Issue{Number: 7, Title: "Fix retry loss", Body: "Preserve pending work after restart", State: "OPEN", Labels: []string{"whirlwind:ready"}}
	first, err := s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.IngestIssue(ctx, p, issue)
	if err != nil || first.ID != again.ID {
		t.Fatalf("duplicate intake: %+v %v", again, err)
	}
	if again.Stage != "planning" || !again.HasSelection() {
		t.Fatalf("unselected issue can run: %+v", again)
	}
	candidates, err := s.List(ctx, "directions")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidate count=%d: %v", len(candidates), err)
	}

}

func TestSavedDiscoveryRejectsReintroducedDismissedEvidence(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first := model.Direction{ID: "first", ProjectID: "p", Title: "Repair retry loss", Fingerprint: "same-evidence", Status: "dismissed"}
	if err := s.Put(ctx, "directions", first.ID, first, 0); err != nil {
		t.Fatal(err)
	}
	regenerated := first
	regenerated.ID = "second"
	regenerated.Status = "proposed"
	if err := s.SaveDirections(ctx, "p", "2026-10-22", []model.Direction{regenerated}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dismissed proposal was reintroduced: %v", err)
	}
	items, err := s.List(ctx, "directions")
	if err != nil || len(items) != 1 {
		t.Fatalf("dismissed evidence reintroduced: %d %v", len(items), err)
	}
	saved, _ := Decode[model.Direction](items[0])
	if saved.Status != "dismissed" {
		t.Fatalf("dismissal overwritten: %+v", saved)
	}
}
