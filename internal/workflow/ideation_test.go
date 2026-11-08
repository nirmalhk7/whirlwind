package workflow

import (
	"context"
	"encoding/json"
	"errors"
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
		{name: "repeated description", mutate: func(d []model.Direction) { d[1].Description = d[0].Description }, invalid: true},
		{name: "normalized historical title", previous: []model.Direction{{Title: " SLACK--integration!! "}}, invalid: true},
		{name: "renamed historical description", previous: []model.Direction{{Title: "Different name", Description: "Send TASK completion notifications to Slack!"}}, invalid: true},
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
	for _, category := range directionCategories {
		if seen[category] != 1 {
			t.Fatalf("category %s count=%d, want one: %v", category, seen[category], seen)
		}
	}
}

type ideationFixture struct {
	batch          []model.Direction
	batches        [][]model.Direction
	generations    int
	reviews        []string
	calls          int
	reviewPrompts  []string
	reviewSessions []string
	worktrees      []string
	commits        []string
}

func (f *ideationFixture) Run(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
	f.calls++
	f.worktrees = append(f.worktrees, in.CWD)
	commit, err := exec.Command("git", "-C", in.CWD, "rev-parse", "HEAD").Output()
	if err == nil {
		f.commits = append(f.commits, strings.TrimSpace(string(commit)))
	}
	if strings.Contains(in.Prompt, "Candidate ideas:") {
		f.reviewPrompts = append(f.reviewPrompts, in.Prompt)
		f.reviewSessions = append(f.reviewSessions, in.SessionID)
		if len(f.reviews) == 0 {
			return provider.Output{}, errors.New("review unavailable")
		}
		out := f.reviews[0]
		f.reviews = f.reviews[1:]
		return provider.Output{Text: out}, nil
	}
	batch := f.batch
	if f.generations < len(f.batches) {
		batch = f.batches[f.generations]
	}
	f.generations++
	raw, _ := json.Marshal(batch)
	return provider.Output{Text: string(raw), SessionID: "generator-session"}, nil
}

func TestGenerationAndNoveltyUseDistinctPinnedGitWorktrees(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	if err := prepared.engine.Ideate(context.Background(), prepared.project, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	if len(prepared.fixture.worktrees) != 2 || len(prepared.fixture.commits) != 2 {
		t.Fatalf("expected generator and novelty worktree calls, paths=%v commits=%v", prepared.fixture.worktrees, prepared.fixture.commits)
	}
	if prepared.fixture.worktrees[0] == prepared.fixture.worktrees[1] {
		t.Fatalf("generation and novelty shared working directory %q", prepared.fixture.worktrees[0])
	}
	if prepared.fixture.commits[0] != prepared.fixture.commits[1] {
		t.Fatalf("agent worktrees were not pinned to same revision: %v", prepared.fixture.commits)
	}
	for _, path := range prepared.fixture.worktrees {
		if filepath.Clean(path) == filepath.Clean(prepared.project.Path) {
			t.Fatalf("agent used the primary checkout: %q", path)
		}
	}
}

func TestIdeationNoveltyGate(t *testing.T) {
	for _, tc := range []noveltyTestCase{
		{"unique", []string{`{"unique":true}`}, true},
		{"rephrase rejected", []string{`{"unique":false,"reason":"Same outcome as historical idea"}`, `{"unique":false}`, `{"unique":false}`}, false},
		{"regenerate after rejection", []string{`{"unique":false}`, `{"unique":true}`}, true},
		{"missing verdict", []string{`{}`}, false},
		{"malformed review", []string{`not JSON`}, false},
		{"null verdict", []string{`{"unique":null}`}, false},
		{"unavailable", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifyNoveltyGate(t, tc)
		})
	}
}

type noveltyTestCase struct {
	name    string
	reviews []string
	success bool
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
	fixture := &ideationFixture{batch: ideaBatch(t), reviews: reviews}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return noveltyTestFixture{engine: e, project: project, fixture: fixture, historical: historical, other: other}
}

func verifyNoveltyGate(t *testing.T, tc noveltyTestCase) {
	t.Helper()
	prepared := prepareNoveltyTest(t, tc.reviews)
	e, project, fixture, historical, other := prepared.engine, prepared.project, prepared.fixture, prepared.historical, prepared.other
	if tc.name == "regenerate after rejection" {
		regenerated := ideaBatch(t)
		for index := range regenerated {
			regenerated[index].Problem = "Newly observed " + regenerated[index].Category + " issue in the repository"
			regenerated[index].Mechanism = "Use a distinct " + regenerated[index].Category + " remediation path"
			regenerated[index].Components = []string{"internal/recovery/" + regenerated[index].Category}
		}
		fixture.batches = [][]model.Direction{fixture.batch, regenerated}
	}
	ctx := context.Background()
	err := e.Ideate(ctx, project, "2026-10-13")
	if (err == nil) != tc.success {
		t.Fatalf("success=%v error=%v", tc.success, err)
	}
	records, _ := e.Store.List(ctx, "directions")
	want := 2
	if tc.success {
		want = 6
	}
	if len(records) != want {
		t.Fatalf("saved %d directions, want %d", len(records), want)
	}
	assertNoveltyReviewInputs(t, fixture, historical, other)
	if tc.success {
		assertNoveltyHistoryNotRepeated(t, e, project)
	}
	if !tc.success {
		record, err := e.Store.Get(ctx, "schedules", project.ID+":2026-10-13")
		if err != nil {
			t.Fatalf("incomplete batch state not retained: %v", err)
		}
		var state struct {
			Stage string `json:"stage"`
		}
		if err := json.Unmarshal(record.Data, &state); err != nil {
			t.Fatal(err)
		}
		if state.Stage != "incomplete" {
			t.Fatalf("invalid batch stage=%q", state.Stage)
		}
	}
}

func assertNoveltyReviewInputs(t *testing.T, fixture *ideationFixture, historical, other model.Direction) {
	t.Helper()
	if len(fixture.reviewPrompts) == 0 {
		t.Fatal("missing semantic review")
	}
	for _, session := range fixture.reviewSessions {
		if session != "" {
			t.Fatalf("novelty review reused generation session %q", session)
		}
	}
	for _, prompt := range fixture.reviewPrompts {
		if strings.Contains(prompt, other.Title) {
			t.Fatalf("wrong project history: %s", prompt)
		}
	}
}

func assertNoveltyHistoryNotRepeated(t *testing.T, e *Engine, project model.Project) {
	t.Helper()
	ctx := context.Background()
	// Reusing the same batch on a later day must be rejected against saved history.
	if err := e.Ideate(ctx, project, "2026-10-14"); err == nil || !strings.Contains(err.Error(), "categories:") {
		t.Fatalf("unchanged discovery was treated as a successful four-category run: %v", err)
	} else {
		for _, category := range directionCategories {
			if !strings.Contains(err.Error(), category) {
				t.Fatalf("incomplete repeat omitted category %q: %v", category, err)
			}
		}
	}
	after, err := e.Store.List(ctx, "directions")
	if err != nil || len(after) != 6 {
		t.Fatalf("repeat changed saved history: %d %v", len(after), err)
	}
}
