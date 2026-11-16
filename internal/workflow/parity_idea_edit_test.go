package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type ideaEditor interface {
	UpdateIdea(context.Context, string, int, model.IdeaDraft, *store.Receipt) error
}

func editableIdeaFixture(t *testing.T, verdict string) (noveltyTestFixture, model.Record, model.Idea) {
	t.Helper()
	f := prepareNoveltyTest(t, []string{`{"unique":true}`, verdict})
	if err := f.engine.Ideate(context.Background(), f.project, "2026-11-11"); err != nil {
		t.Fatal(err)
	}
	records, err := f.engine.Store.List(context.Background(), "ideas")
	if err != nil || len(records) != 4 {
		t.Fatalf("idea fixture missing: %d %v", len(records), err)
	}
	i, err := store.Decode[model.Idea](records[0])
	if err != nil {
		t.Fatal(err)
	}
	return f, records[0], i
}

func TestParityIdeaSimilarDraftEditSavesWithoutNoveltyComparison(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":true}`)
	editor, ok := any(f.engine).(ideaEditor)
	if !ok {
		t.Fatal("draft edit novelty validation is unavailable")
	}
	draft := model.IdeaDraft{What: "Reword an earlier outcome", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	calls := f.fixture.calls
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); err != nil {
		t.Fatalf("draft edit was blocked by novelty comparison: %v", err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil || after.Version != r.Version+1 || f.fixture.calls != calls {
		t.Fatalf("draft edit version or provider call mismatch: v%d err=%v calls=%d want=%d", after.Version, err, f.fixture.calls, calls)
	}
	saved, err := store.Decode[model.Idea](after)
	if err != nil || saved.What != draft.What || saved.NoveltyMethod != "not_checked" || saved.NoveltyOutcome != "deferred" {
		t.Fatalf("draft edit comparison state is inaccurate: %+v %v", saved, err)
	}
}

func TestParityIdeaUniqueDraftEditUpdatesSearchAndRetainsSourceRun(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":true}`)
	editor, ok := any(f.engine).(ideaEditor)
	if !ok {
		t.Fatal("draft edit service is unavailable")
	}
	draft := model.IdeaDraft{What: "QuasarExport monthly invoices", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	calls := f.fixture.calls
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); err != nil {
		t.Fatal(err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Idea](after)
	if err != nil || saved.What != draft.What || saved.Fingerprint == i.Fingerprint || saved.RunID != i.RunID || after.Version != r.Version+1 || saved.DecisionStatus != "draft" || saved.NoveltyMethod != "not_checked" || saved.NoveltyOutcome != "deferred" || f.fixture.calls != calls {
		t.Fatalf("edit lost content, identity or decision: %+v %v", saved, err)
	}
	assertIdeaSearchContent(t, f.engine, i, draft.What)
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
}

func TestParityIdeaEditRespectsProjectGenerationLease(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":true}`)
	ctx := context.Background()
	leaseID := "generation:" + i.ProjectID
	lease, err := f.engine.Store.Claim(ctx, leaseID, "other-editor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.engine.Store.Release(context.Background(), leaseID, "other-editor", lease.Fence) }()
	draft := model.IdeaDraft{What: "Changed while being approved", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	calls := f.fixture.calls
	if err := f.engine.UpdateIdea(ctx, i.ID, r.Version, draft, nil); !errors.Is(err, store.ErrLeased) {
		t.Fatalf("active generation lease did not serialize draft edit: %v", err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Idea](after)
	if err != nil || saved.What != i.What || saved.Fingerprint != i.Fingerprint || saved.DecisionStatus != "draft" || after.Version != r.Version || f.fixture.calls != calls {
		t.Fatalf("leased edit changed saved draft or launched model: %+v v%d calls=%d err=%v", saved, after.Version, f.fixture.calls, err)
	}
}

func assertIdeaSearchContent(t *testing.T, engine *Engine, i model.Idea, what string) {
	t.Helper()
	matches, err := engine.Store.SearchProposals(context.Background(), model.ProposalQuery{ProjectID: i.ProjectID, Terms: "QuasarExport"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range matches {
		if match.ID == i.ID && match.Title == i.Title && match.IdeaFields != nil && match.IdeaFields.What == what {
			found = true
		}
	}
	if !found {
		t.Fatal("accepted edit was not reflected in novelty search")
	}
	var indexed int
	if err := engine.Store.DB.QueryRow("SELECT count(*) FROM proposals_fts WHERE project_id=? AND proposal_id=? AND proposals_fts MATCH ?", i.ProjectID, i.ID, "QuasarExport").Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("accepted What edit missing from full-text index: %d %v", indexed, err)
	}

}
