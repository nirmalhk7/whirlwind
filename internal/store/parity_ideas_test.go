package store

import (
	"context"
	"errors"
	"testing"
	"whirlwind/internal/model"
)

const (
	ideaFixtureExportTitle = "Export invoices"
)

func TestParityIdeaRegistryHasDurableConstrainedTables(t *testing.T) {
	s := openTest(t)
	for _, table := range []string{"ideas", "ideation_runs", "ideation_directions", "idea_candidates", "planner_runs", "plans", "plan_versions"} {
		var found int
		if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&found); err != nil || found != 1 {
			t.Fatalf("durable %s registry unavailable: count=%d err=%v", table, found, err)
		}
	}
	ctx := context.Background()
	if err := s.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO ideation_runs(run_id,project_id,status,created_at) VALUES('r','p','queued','now')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO ideas(idea_id,project_id,run_id,idea_title,idea_type,what,why,"where",how,status,timestamp) VALUES('i','p','r','Invoice export','expand','Export','Reconcile','billing','Keep records','selected','now')`); err == nil {
		t.Fatal("registry accepted sixth idea lifecycle status")
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO idea_candidates(candidate_id,run_id,direction,ordinal,status) VALUES('c','r','expand',6,'generated')"); err == nil {
		t.Fatal("registry admitted sixth generated candidate")
	}
}

func parityIdea(t *testing.T, s *Store) map[string]any {
	t.Helper()
	if err := s.Put(context.Background(), "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0); err != nil && !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO ideation_runs(run_id,project_id,status,created_at) VALUES('source','p','running','now')"); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"idea_title": "Invoice exports", "idea_id": "i", "project_id": "p", "project_name": "Project", "run_id": "source", "direction": "expand", "what": ideaFixtureExportTitle, "why": "Accounting needs invoices", "where": "billing", "how_it_helps": "Export one month together", "decision_status": "draft", "novelty_method": "legacy_llm", "novelty_outcome": "accepted"}
}

func TestParityIdeaProjectionAndReadOnlyApproval(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	var what, state string
	if err := s.DB.QueryRow("SELECT what,status FROM ideas WHERE idea_id='i'").Scan(&what, &state); err != nil || what != ideaFixtureExportTitle || state != "draft" {
		t.Fatalf("idea publication missing from registry: %s %s %v", what, state, err)
	}
	idea["decision_status"] = "approved"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err != nil {
		t.Fatal(err)
	}
	idea["what"] = "Changed after approval"
	if err := s.Put(ctx, "ideas", "i", idea, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("approved content changed: %v", err)
	}
	var version int
	if err := s.DB.QueryRow("SELECT i.what,r.version FROM ideas i JOIN resources r ON r.kind='ideas' AND r.id=i.idea_id WHERE i.idea_id='i'").Scan(&what, &version); err != nil || what != ideaFixtureExportTitle || version != 2 {
		t.Fatalf("failed edit changed registry: %s v%d %v", what, version, err)
	}
}

func TestParityArchivedDraftRetainsItsContentAndEligibility(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	idea["archived_at"], idea["archive_reason"] = "2026-11-10T00:00:00Z", "Human rejected"
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["what"] = "Rewrite archived history"
	if err := s.Put(ctx, "ideas", "i", idea, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived history was rewritten: %v", err)
	}
}

func TestParityIdeaLifecycleRequiresExplicitOrderedTransitions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	version := 1
	for _, status := range []string{"approved", "in_process", "error", "approved", "in_process", "complete"} {
		idea["decision_status"] = status
		if err := s.Put(ctx, "ideas", "i", idea, version); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		version++
	}
	idea["decision_status"] = "approved"
	if err := s.Put(ctx, "ideas", "i", idea, version); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed idea restarted: %v", err)
	}
}

func TestParityIdeaSQLRetainsSupportingMetadata(t *testing.T) {
	s := openTest(t)
	idea := parityIdea(t, s)
	idea["components"] = []string{"billing/export.go", "billing/invoice.go"}
	idea["mechanism"] = "Batch invoices by month"
	if err := s.Put(context.Background(), "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	var where, components, mechanism string
	if err := s.DB.QueryRow(`SELECT i."where",json_extract(r.data,'$.components'),json_extract(r.data,'$.mechanism') FROM ideas i JOIN resources r ON r.kind='ideas' AND r.id=i.idea_id WHERE i.idea_id='i'`).Scan(&where, &components, &mechanism); err != nil || where != "billing" || components != `["billing/export.go","billing/invoice.go"]` || mechanism != "Batch invoices by month" {
		t.Fatalf("SQL idea metadata missing: %q %q %q %v", where, components, mechanism, err)
	}
}
