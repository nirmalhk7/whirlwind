package workflow

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

func ideaBatch(t *testing.T) []model.Direction {
	t.Helper()
	var ideas []model.Direction
	err := json.Unmarshal([]byte(`[
 {"category":"expand","title":"Slack integration","description":"Send task completion notifications to Slack."},
 {"category":"optimize","title":"Cache repository index","description":"Reuse unchanged file indexes to reduce scan latency."},
 {"category":"harden","title":"Recover expired leases","description":"Reconcile abandoned work after worker crashes."},
 {"category":"simplify","title":"Consolidate routing","description":"Replace duplicated route registration with one table."}
 ]`), &ideas)
	if err != nil {
		t.Fatal(err)
	}
	for n := range ideas {
		ideas[n].Evidence = []model.EvidenceRef{{Kind: "code", Ref: ".whirlwind.yaml"}}
		ideas[n].Problem = "Current " + ideas[n].Category + " problem in the repository"
		ideas[n].Mechanism = "Apply the " + ideas[n].Category + " change to resolve that problem"
		ideas[n].Components = []string{"internal/" + ideas[n].Category}
		ideas[n].RepositoryRevision = "current-revision"
		ideas[n].ExpectedBenefit = "Avoid repeated work"
		ideas[n].EffortEstimate = "Small scoped change"
		ideas[n].Uncertainty = []string{"Validate against representative workload"}
		ideas[n].ProblemKind = "opportunity"
	}
	return ideas
}

func TestValidateDirections(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func([]model.Direction)
		previous []model.Direction
		invalid  bool
	}{
		{name: "four categories"},
		{name: "missing category", mutate: func(d []model.Direction) { d[0] = model.Direction{Title: d[0].Title, Description: d[0].Description} }, invalid: true},
		{name: "blank description", mutate: func(d []model.Direction) { d[0].Description = " \n\t" }, invalid: true},
		{name: "repeated description", mutate: func(d []model.Direction) { d[1].Description = d[0].Description }},
		{name: "normalized historical title", previous: []model.Direction{{Title: " SLACK--integration!! "}}},
		{name: "renamed historical description", previous: []model.Direction{{Title: "Different name", Description: "Send TASK completion notifications to Slack!"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := ideaBatch(t)
			if tc.mutate != nil {
				tc.mutate(batch)
			}
			err := validateDirections(batch, tc.previous)
			if (err != nil) != tc.invalid {
				t.Fatalf("invalid=%v, error=%v", tc.invalid, err)
			}
		})
	}
	// Decode category variants through the same JSON boundary as provider output.
	for _, category := range []string{"unknown", ""} {
		batch := ideaBatch(t)
		raw, _ := json.Marshal(batch)
		raw = []byte(strings.Replace(string(raw), `"category":"optimize"`, `"category":"`+category+`"`, 1))
		_ = json.Unmarshal(raw, &batch)
		if err := validateDirections(batch, nil); err == nil {
			t.Fatalf("accepted duplicate/unknown category %q", category)
		}
	}
}

func TestValidateDirectionsRejectsTwoIdeasInOneCategory(t *testing.T) {
	batch := ideaBatch(t)
	batch[1].Category = "expand"
	if err := validateDirections(batch, nil); err == nil {
		t.Fatal("two candidates in one category were accepted")
	}
}

func TestIncompleteFourCategoryGenerationIsSavedButReturnsActionableError(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	prepared.fixture.batch = ideaBatch(t)[:2]
	err := prepared.engine.Ideate(context.Background(), prepared.project, "2026-10-30")
	if err == nil || !strings.Contains(err.Error(), "harden") || !strings.Contains(err.Error(), "simplify") {
		t.Fatalf("partial ideation did not report missing categories: %v", err)
	}
	rows, err := prepared.engine.Store.List(context.Background(), "directions")
	if err != nil || len(rows) != 4 { // two legacy fixture records plus two generated candidates
		t.Fatalf("partial proposals were not retained: count=%d error=%v", len(rows), err)
	}
	schedule, err := prepared.engine.Store.Get(context.Background(), "schedules", prepared.project.ID+":2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Stage   string   `json:"stage"`
		Missing []string `json:"missing_categories"`
	}
	if err := json.Unmarshal(schedule.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Stage != "incomplete" || !strings.Contains(strings.Join(state.Missing, ","), "harden") {
		t.Fatalf("partial schedule is not recoverable: %+v", state)
	}
}

func TestIdeationRepairsOnlyMissingCategoriesAndPreservesAcceptedCandidates(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`, `{"unique":true}`})
	batch := ideaBatch(t)
	prepared.fixture.batches = [][]model.Direction{batch[:1], {batch[0], batch[1], batch[2], batch[3]}}
	if err := prepared.engine.Ideate(context.Background(), prepared.project, "2026-11-01"); err != nil {
		t.Fatalf("valid repaired four-category run failed: %v", err)
	}
	if prepared.fixture.generations != 2 {
		t.Fatalf("wanted initial and missing-category repair calls, got %d", prepared.fixture.generations)
	}
	rows, err := prepared.engine.Store.List(context.Background(), "directions")
	if err != nil || len(rows) != 6 {
		t.Fatalf("accepted candidates were lost or duplicated: count=%d err=%v", len(rows), err)
	}
	seen := map[string]int{}
	for _, row := range rows {
		direction, decodeErr := store.Decode[model.Direction](row)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if direction.Day == "2026-11-01" {
			seen[direction.Category]++
		}
	}
	for _, category := range model.DirectionCategories() {
		if seen[category] != 1 {
			t.Fatalf("category %s count=%d, want one: %v", category, seen[category], seen)
		}
	}
}

type ideationFixture struct {
	batch             []model.Direction
	batches           [][]model.Direction
	generations       int
	calls             int
	generationPrompts []string
	worktrees         []string
	commits           []string
}

func (f *ideationFixture) Run(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
	f.calls++
	f.worktrees = append(f.worktrees, in.CWD)
	commit, err := exec.Command("git", "-C", in.CWD, "rev-parse", "HEAD").Output()
	if err == nil {
		f.commits = append(f.commits, strings.TrimSpace(string(commit)))
	}
	f.generationPrompts = append(f.generationPrompts, in.Prompt)
	batch := f.batch
	if f.generations < len(f.batches) {
		batch = f.batches[f.generations]
	}
	f.generations++
	raw, _ := json.Marshal(batch)
	return provider.Output{Text: string(raw), SessionID: "generator-session"}, nil
}

func TestGenerationUsesOnePinnedGitWorktreeWithoutNoveltyJudge(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	if err := prepared.engine.Ideate(context.Background(), prepared.project, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	if len(prepared.fixture.worktrees) != 1 || len(prepared.fixture.commits) != 1 || prepared.fixture.generations != 1 {
		t.Fatalf("expected one generation worktree call, paths=%v commits=%v generations=%d", prepared.fixture.worktrees, prepared.fixture.commits, prepared.fixture.generations)
	}
	for _, path := range prepared.fixture.worktrees {
		if filepath.Clean(path) == filepath.Clean(prepared.project.Path) {
			t.Fatalf("agent used the primary checkout: %q", path)
		}
	}
}

func TestIdeationPublishesHistoricalFingerprintMatchWithoutComparison(t *testing.T) {
	prepared := prepareNoveltyTest(t, nil)
	ctx := context.Background()
	candidate := prepared.fixture.batch[2]
	fingerprint := model.ProposalFingerprint(prepared.project.ID, candidate.Problem, candidate.Mechanism, candidate.Components)
	historical := model.Proposal{ID: "historical-fingerprint-match", ProjectID: prepared.project.ID, Category: candidate.Category, Problem: candidate.Problem, Mechanism: candidate.Mechanism, Components: candidate.Components, Fingerprint: fingerprint, Title: "Previously recorded idea", Description: candidate.Description, Status: "implemented"}
	if err := prepared.engine.Store.RecordProposalRejection(ctx, historical, "historical record retained"); err != nil {
		t.Fatal(err)
	}
	if err := prepared.engine.Ideate(ctx, prepared.project, "2026-10-13"); err != nil {
		t.Fatalf("historical fingerprint match blocked ideation: %v", err)
	}
	ideas, err := prepared.engine.Store.List(ctx, "ideas")
	if err != nil || len(ideas) != 4 {
		t.Fatalf("generated ideas not published: count=%d err=%v", len(ideas), err)
	}
	var matching *model.Idea
	for _, record := range ideas {
		idea, decodeErr := store.Decode[model.Idea](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if idea.Direction == candidate.Category {
			matching = &idea
		}
	}
	if matching == nil || matching.NoveltyMethod != "not_checked" || matching.NoveltyOutcome != "deferred" {
		t.Fatalf("generated comparison state is inaccurate: %+v", matching)
	}
}

type noveltyTestFixture struct {
	engine            *Engine
	project           model.Project
	fixture           *ideationFixture
	historical, other model.Direction
}

func prepareNoveltyTest(t *testing.T, reviews []string) noveltyTestFixture {
	t.Helper()
	openshell.Install(t)
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	p, _ := store.Decode[model.Provider](record)
	p.Capabilities = append(p.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", p, record.Version); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "ideate", model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	historical := model.Direction{ID: "old", ProjectID: task.ProjectID, Title: "Webhook completion alerts", Description: "Notify Slack when a task finishes."}
	if err := e.Store.Put(ctx, "directions", historical.ID, historical, 0); err != nil {
		t.Fatal(err)
	}
	other := model.Direction{ID: "other", ProjectID: "other-project", Title: "Other project only"}
	if err := e.Store.Put(ctx, "directions", other.ID, other, 0); err != nil {
		t.Fatal(err)
	}
	_ = reviews // Retained as a fixture signature for tests sharing older setup helpers.
	fixture := &ideationFixture{batch: ideaBatch(t)}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return noveltyTestFixture{engine: e, project: project, fixture: fixture, historical: historical, other: other}
}
