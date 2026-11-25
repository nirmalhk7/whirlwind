package workflow

import (
	"context"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityGeneratedIdeasPublishDurableContentWithDeferredUniqueness(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	if err := f.engine.Ideate(context.Background(), f.project, "2026-11-11"); err != nil {
		t.Fatal(err)
	}
	ideas, err := f.engine.Store.List(context.Background(), "ideas")
	if err != nil || len(ideas) != 4 {
		t.Fatalf("generated ideas were not durably published: count=%d err=%v", len(ideas), err)
	}
	var accepted int
	if err := f.engine.Store.DB.QueryRow("SELECT count(*) FROM idea_candidates WHERE status='accepted'").Scan(&accepted); err != nil || accepted != 4 {
		t.Fatalf("candidate publication ledger missing accepted ideas: count=%d err=%v", accepted, err)
	}
	for _, record := range ideas {
		idea, decodeErr := store.Decode[model.Idea](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if idea.NoveltyMethod != "not_checked" || idea.NoveltyOutcome != "deferred" {
			t.Fatalf("new idea overstated uniqueness assessment: %+v", idea)
		}
	}
}
