package workflow

import (
	"context"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func TestIdeationHistoryFailures(t *testing.T) {
	for _, broken := range []string{"unavailable", "corrupt"} {
		t.Run(broken, func(t *testing.T) {
			e, task, _ := testEngine(t)
			ctx := context.Background()
			if broken == "unavailable" {
				_ = e.Store.Close()
			} else {
				proposal := model.Proposal{ID: "broken", ProjectID: task.ProjectID, Category: "harden", Problem: "Corrupt saved history", Mechanism: "Expose history decode errors", Components: []string{"internal/workflow"}, Title: "Broken history", Description: "Corruption test", Status: "shown"}
				if err := e.Store.RecordProposalRejection(ctx, proposal, "Corrupt history fixture"); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Store.DB.ExecContext(ctx, "UPDATE proposals SET data=? WHERE project_id=? AND id=?", `"not a proposal"`, task.ProjectID, proposal.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.previousDirections(ctx, task.ProjectID); err == nil {
				t.Fatal("unreadable history accepted")
			}
		})
	}
}

func TestValidateDirectionCount(t *testing.T) {
	batch := ideaBatch(t)
	for _, ideas := range [][]model.Direction{append(batch, batch[0])} {
		if err := validateDirections(ideas, nil); err == nil {
			t.Fatalf("accepted %d ideas", len(ideas))
		}
	}
	for _, ideas := range [][]model.Direction{nil, batch[:1], batch[:3]} {
		if err := validateDirections(ideas, nil); err != nil {
			t.Fatalf("valid bounded candidates: %v", err)
		}
	}
}

func TestIdeaDescriptionWordBoundary(t *testing.T) {
	for _, tc := range []struct {
		words   int
		invalid bool
	}{{35, false}, {36, true}} {
		batch := ideaBatch(t)
		batch[0].Description = strings.Repeat("word\t\n", tc.words)
		if err := validateDirections(batch, nil); (err != nil) != tc.invalid {
			t.Fatalf("%d words: invalid=%v error=%v", tc.words, tc.invalid, err)
		}
	}
}

func TestOverlongIdeationRetriesWithoutSaving(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	_ = e.Store.Put(ctx, "providers", "fake", source, record.Version)
	batch := ideaBatch(t)
	const overlongMarker = "overlong-description-marker "
	batch[0].Description = strings.Repeat(overlongMarker, 36)
	fixture := &ideationFixture{batch: batch, reviews: []string{`{"unique":true}`}}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	if err := e.Ideate(ctx, project, "2026-10-14"); err == nil || !strings.Contains(err.Error(), "no worthwhile new direction") {
		t.Fatalf("overlong ideas accepted: %v", err)
	}
	if fixture.generations != 3 || len(fixture.reviewPrompts) != 1 {
		t.Fatalf("generation retries=%d novelty reviews=%d", fixture.generations, len(fixture.reviewPrompts))
	}
	for _, prompt := range fixture.reviewPrompts {
		if strings.Contains(prompt, overlongMarker) {
			t.Fatal("overlong candidate reached novelty review instead of being rejected during generation")
		}
	}
	directions, _ := e.Store.List(ctx, "directions")
	if len(directions) != 3 {
		t.Fatalf("valid partial candidates were not retained: got %d", len(directions))
	}
	for _, row := range directions {
		direction, err := store.Decode[model.Direction](row)
		if err != nil {
			t.Fatal(err)
		}
		if direction.Category == "expand" || strings.Contains(direction.Description, overlongMarker) || len(strings.Fields(direction.Description)) > agents.MaxIdeaDescriptionWords {
			t.Fatalf("overlong candidate was persisted: %+v", direction)
		}
	}
	schedule, err := e.Store.Get(ctx, "schedules", project.ID+":2026-10-14")
	if err != nil {
		t.Fatalf("incomplete schedule state was not saved: %v", err)
	}
	state, err := store.Decode[map[string]any](schedule)
	if err != nil || state["stage"] != "incomplete" {
		t.Fatalf("partial ideation was not recorded as incomplete: %+v %v", state, err)
	}
	if missing, ok := state["missing_categories"].([]any); !ok || len(missing) != 1 || missing[0] != "expand" {
		t.Fatalf("incomplete schedule did not identify overlong category: %+v", state["missing_categories"])
	}
}
