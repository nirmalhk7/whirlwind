package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"whirlwind/internal/model"
)

const (
	cleanupComponentA = "src/a.go"
	cleanupComponentB = "src/b.go"
)

func TestComponentSearchAndCountsUseProposalJSON(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, d := range []model.Direction{
		{ID: "one", ProjectID: "p", Category: "expand", Problem: "first", Mechanism: "first", Components: []string{" src/a.go ", "\u2003src/a.go\u00a0", "", "\t", cleanupComponentB}},
		{ID: "two", ProjectID: "p", Category: "harden", Problem: "second", Mechanism: "second", Components: []string{cleanupComponentA}},
		{ID: "other", ProjectID: "other", Category: "expand", Problem: "third", Mechanism: "third", Components: []string{cleanupComponentA}},
	} {
		if err := s.SaveDirections(ctx, d.ProjectID, "2026-11-11", []model.Direction{d}); err != nil {
			t.Fatal(err)
		}
	}
	// Removing the derived table must not break history reads.
	if _, err := s.DB.Exec("DROP TABLE IF EXISTS proposal_components"); err != nil {
		t.Fatal(err)
	}
	counts, err := s.ProposalComponentCounts(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(counts, map[string]int{cleanupComponentA: 2, cleanupComponentB: 1}) {
		t.Fatalf("counts: %#v", counts)
	}
	for _, tc := range []struct {
		components []string
		want       int
	}{
		{[]string{cleanupComponentB}, 1},
		{[]string{cleanupComponentA}, 2},
		{[]string{cleanupComponentA, cleanupComponentB}, 2},
		{[]string{"missing.go"}, 0},
		{[]string{""}, 0},
		{[]string{"", "missing.go"}, 0},
		{[]string{"", cleanupComponentB}, 1},
	} {
		found, err := s.SearchProposals(ctx, model.ProposalQuery{ProjectID: "p", Components: tc.components})
		if err != nil || len(found) != tc.want {
			t.Fatalf("search %q: %d %v", tc.components, len(found), err)
		}
	}
}

func TestComponentTableRemovedFromFreshAndReopenedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='proposal_components'").Scan(&count); err != nil || count != 0 {
		_ = s.Close()
		t.Fatalf("duplicate component table remains: %d %v", count, err)
	}
	direction := model.Direction{ID: "kept", ProjectID: "p", Category: "expand", Components: []string{"src/kept.go"}}
	if err := s.SaveDirections(context.Background(), "p", "2026-11-11", []model.Direction{direction}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("CREATE TABLE proposal_components(project_id TEXT,component TEXT,proposal_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='proposal_components'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("obsolete component table remains on reopen: %d %v", count, err)
	}
	counts, err := s.ProposalComponentCounts(context.Background(), "p")
	if err != nil || !reflect.DeepEqual(counts, map[string]int{"src/kept.go": 1}) {
		t.Fatalf("reopen lost component history: %#v %v", counts, err)
	}
}
