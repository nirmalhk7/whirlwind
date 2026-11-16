package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestParityGeneratorPreservesExplicitFourFieldsAndSeparateEvidence(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	f.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			if in.Purpose != "generation" {
				return f.fixture.Run(ctx, in, h)
			}
			return explicitIdeaGenerationOutput(t), nil
		})
	}
	if err := f.engine.Ideate(ctx, f.project, "2026-11-11"); err != nil {
		t.Fatalf("explicit idea schema unsupported: %v", err)
	}
	records, err := f.engine.Store.List(ctx, "ideas")
	if err != nil || len(records) != 4 {
		t.Fatalf("missing explicit ideas: %d %v", len(records), err)
	}
	for _, record := range records {
		idea, err := store.Decode[model.Idea](record)
		requireWorkflowSuccess(t, err)
		if idea.Where != "billing/"+idea.Direction+".go" || idea.What == "" || idea.Why == "" || idea.HowItHelps == "" || idea.Mechanism == "" || len(idea.Components) == 0 || len(idea.Evidence) == 0 || idea.RunID == "" {
			t.Fatalf("explicit fields/provenance collapsed into supporting metadata: %+v", idea)
		}
		var revision, configuration string
		if err := f.engine.Store.DB.QueryRow("SELECT repository_revision,configuration_hash FROM ideation_runs WHERE run_id=?", idea.RunID).Scan(&revision, &configuration); err != nil || revision != idea.RepositoryRevision || len(configuration) != 64 {
			t.Fatalf("source run missing pinned repository/configuration identity: %s %s %v", revision, configuration, err)
		}

		assertExplicitIdeaNoveltyHistory(t, ctx, f.engine, f.project.ID, idea)

	}
}

func explicitIdeaGenerationOutput(t *testing.T) provider.Output {
	t.Helper()
	var candidates []map[string]any
	for _, legacy := range ideaBatch(t) {
		raw, _ := json.Marshal(legacy)
		var candidate map[string]any
		_ = json.Unmarshal(raw, &candidate)
		delete(candidate, "category")
		delete(candidate, "title")
		delete(candidate, "problem")
		delete(candidate, "description")
		candidate["direction"], candidate["what"], candidate["why"], candidate["where"], candidate["how_it_helps"] = legacy.Category, "Explicit "+legacy.Title, legacy.Problem, "billing/"+legacy.Category+".go", legacy.Description
		candidates = append(candidates, candidate)
	}
	raw, _ := json.Marshal(candidates)
	return provider.Output{Text: string(raw)}
}

func assertExplicitIdeaNoveltyHistory(t *testing.T, ctx context.Context, engine *Engine, projectID string, idea model.Idea) {
	t.Helper()
	history, err := engine.previousDirections(ctx, projectID, idea.Where)
	if err != nil || !strings.Contains(jsonText(compactProposalHistory(history)), idea.Where) || !strings.Contains(jsonText(compactProposalHistory(history)), idea.HowItHelps) {
		t.Fatalf("explicit location/benefit missing from novelty history: %s %v", jsonText(compactProposalHistory(history)), err)
	}
}
