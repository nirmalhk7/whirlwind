package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	registrySourceRunID = "source-run"
)

func TestParityPartialGenerationPublishesIdeaBeforeLaterFailures(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	prepared.fixture.batch = ideaBatch(t)[:1]
	e := prepared.engine
	ctx := context.Background()
	op := model.Operation{Kind: "ideation", Subject: prepared.project.ID, Stage: "running", StartedAt: "now"}
	if err := e.Store.Put(ctx, "operations", registrySourceRunID, op, 0); err != nil {
		t.Fatal(err)
	}
	_ = e.Ideate(WithOperation(ctx, registrySourceRunID), prepared.project, "2026-11-10")
	ideas, err := e.Store.List(ctx, "ideas")
	if err != nil || len(ideas) != 1 {
		t.Fatalf("successful direction lost after later failures: %d %v", len(ideas), err)
	}
	idea, err := store.Decode[model.Idea](ideas[0])
	if err != nil || idea.RunID != registrySourceRunID || idea.DecisionStatus != "draft" || idea.NoveltyMethod != "legacy_llm" {
		t.Fatalf("idea provenance or temporary gate missing: %+v %v", idea, err)
	}
	var count int
	if err := e.Store.DB.QueryRow("SELECT count(*) FROM idea_candidates WHERE run_id='source-run' AND status='accepted'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("accepted candidate evidence missing: %d %v", count, err)
	}
}
