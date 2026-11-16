package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const (
	candidateFixtureDay = "2026-11-11"
)

func TestParityGeneratedCandidatesPersistBeforeNovelty(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	checked := false
	f.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
			if strings.HasPrefix(input.Purpose, "novelty:") {
				assertDurableCandidatesBeforeNovelty(t, ctx, f.engine.Store)
				checked = true
				return provider.Output{}, errors.New("novelty infrastructure unavailable")
			}
			return f.fixture.Run(ctx, input, hooks)
		})
	}
	if err := f.engine.Ideate(context.Background(), f.project, candidateFixtureDay); err == nil {
		t.Fatal("novelty infrastructure failure passed")
	}
	if !checked {
		t.Fatal("novelty boundary not exercised")
	}
}

func TestParityGeneratedCandidatesResumeWithoutAnotherGeneration(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := WithOperation(context.Background(), "durable-generation")
	requireWorkflowSuccess(t, f.engine.Store.Put(ctx, "operations", "durable-generation", model.Operation{Kind: "ideation", Subject: f.project.ID, Stage: "running", Owner: f.engine.Owner()}, 0))
	requireWorkflowSuccess(t, f.engine.pinIdeationRunIdentity(ctx, f.project))
	available, generations := false, 0
	f.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
			if input.Purpose == "generation" {
				generations++
			}
			if strings.HasPrefix(input.Purpose, "novelty:") && !available {
				return provider.Output{}, errors.New("temporary novelty outage")
			}
			return f.fixture.Run(ctx, input, hooks)
		})
	}
	if err := f.engine.generateAndSaveIdeation(ctx, f.project, candidateFixtureDay); err == nil {
		t.Fatal("outage passed")
	}
	if generations != 1 {
		t.Fatalf("unexpected initial generation count %d", generations)
	}
	available = true
	requireWorkflowSuccess(t, f.engine.generateAndSaveIdeation(ctx, f.project, candidateFixtureDay))
	if generations != 1 {
		t.Fatalf("recovery regenerated already durable candidates: %d", generations)
	}
	ideas, err := f.engine.Store.List(ctx, "ideas")
	if err != nil || len(ideas) != 4 {
		t.Fatalf("recovered ideas missing: %d %v", len(ideas), err)
	}
	assertResumedCandidateAllowance(t, f.engine.Store, ideas)
}

func assertDurableCandidatesBeforeNovelty(t *testing.T, ctx context.Context, database *store.Store) {
	t.Helper()
	rows, err := database.DB.QueryContext(ctx, "SELECT fingerprint,content FROM idea_candidates WHERE status='generated'")
	requireWorkflowSuccess(t, err)
	count := 0
	for rows.Next() {
		var fingerprint, content string
		requireWorkflowSuccess(t, rows.Scan(&fingerprint, &content))
		var candidate model.Direction
		if err := json.Unmarshal([]byte(content), &candidate); err != nil || candidate.Title == "" || candidate.Mechanism == "" || len(candidate.Evidence) == 0 || len(fingerprint) != 64 {
			t.Fatalf("candidate content/evidence missing before novelty: %s %s", fingerprint, content)
		}
		count++
	}
	requireWorkflowSuccess(t, rows.Close())
	if count != 4 {
		t.Fatalf("novelty ran before generated candidates became durable: %d", count)
	}
}

func assertResumedCandidateAllowance(t *testing.T, database *store.Store, ideas []model.Record) {
	t.Helper()
	for _, record := range ideas {
		idea, _ := store.Decode[model.Idea](record)
		var ordinal int
		if err := database.DB.QueryRow("SELECT ordinal FROM idea_candidates WHERE candidate_id=?", idea.ID).Scan(&ordinal); err != nil || ordinal != 1 {
			t.Fatalf("restart changed candidate allowance: %d %v", ordinal, err)
		}
	}
}
