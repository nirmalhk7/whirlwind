package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
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

func TestParityIdeaSimilarDraftEditPreservesContentVersionAndHistory(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":false,"reason":"Same outcome as an earlier idea"}`)
	editor, ok := any(f.engine).(ideaEditor)
	if !ok {
		t.Fatal("draft edit novelty validation is unavailable")
	}
	draft := model.IdeaDraft{What: "Reword an earlier outcome", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); err == nil {
		t.Fatal("similar draft edit accepted")
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil || after.Version != r.Version || string(after.Data) != string(r.Data) {
		t.Fatalf("rejected edit changed saved idea: %+v %v", after, err)
	}
}

func TestParityIdeaUniqueDraftEditUpdatesSearchAndRetainsSourceRun(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":true}`)
	editor, ok := any(f.engine).(ideaEditor)
	if !ok {
		t.Fatal("draft edit novelty validation is unavailable")
	}
	draft := model.IdeaDraft{What: "QuasarExport monthly invoices", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); err != nil {
		t.Fatal(err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Idea](after)
	if err != nil || saved.What != draft.What || saved.Fingerprint == i.Fingerprint || saved.RunID != i.RunID || after.Version != r.Version+1 || saved.DecisionStatus != "draft" {
		t.Fatalf("edit lost content, identity or decision: %+v %v", saved, err)
	}
	assertIdeaSearchContent(t, f.engine, i, draft.What)
	prompt := f.fixture.reviewPrompts[len(f.fixture.reviewPrompts)-1]
	_, history, ok := strings.Cut(prompt, "Bounded project proposal history:\n")
	if !ok {
		t.Fatal("novelty history section missing")
	}
	history, _, _ = strings.Cut(history, "Current approved plans for this project:")
	if strings.Contains(history, i.ID) {
		t.Fatal("edited idea compared against itself")
	}
	if err := editor.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
}

type ideaEditRaceAdapter struct {
	provider.Adapter
	before func()
}

func (a ideaEditRaceAdapter) Run(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
	if strings.HasPrefix(input.Purpose, "novelty:") {
		a.before()
	}
	return a.Adapter.Run(ctx, input, hooks)
}

func TestParityIdeaApprovalRacingWithEditRejectsStalePublication(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `{"unique":true}`)
	f.engine.Adapter = func(model.Provider) provider.Adapter {
		return ideaEditRaceAdapter{Adapter: f.fixture, before: func() {
			approved := i
			approved.DecisionStatus = "approved"
			if err := f.engine.Store.Put(context.Background(), "ideas", i.ID, approved, r.Version); err != nil {
				t.Fatal(err)
			}
		}}
	}
	draft := model.IdeaDraft{What: "Changed while being approved", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	if err := f.engine.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("approval did not invalidate edit: %v", err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Idea](after)
	if err != nil || saved.What != i.What || saved.Fingerprint != i.Fingerprint || saved.DecisionStatus != "approved" {
		t.Fatalf("stale edit replaced approved idea: %+v %v", saved, err)
	}
}

func TestParityIdeaNoveltyInfrastructureFailureDoesNotBecomeDuplicate(t *testing.T) {
	f, r, i := editableIdeaFixture(t, `not valid JSON`)
	draft := model.IdeaDraft{What: "Distinct outcome", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}
	err := f.engine.UpdateIdea(context.Background(), i.ID, r.Version, draft, nil)
	var similar *IdeaSimilarityError
	if err == nil || errors.As(err, &similar) {
		t.Fatalf("infrastructure failure fabricated a novelty decision: %v", err)
	}
	after, err := f.engine.Store.Get(context.Background(), "ideas", i.ID)
	if err != nil || after.Version != r.Version || string(after.Data) != string(r.Data) {
		t.Fatalf("failed gate changed saved draft: %+v %v", after, err)
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
