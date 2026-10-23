package workflow

import (
	"context"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestIdeationHistoryFailures(t *testing.T) {
	for _, broken := range []string{"unavailable", "corrupt"} {
		t.Run(broken, func(t *testing.T) {
			e, task, _ := testEngine(t)
			ctx := context.Background()
			if broken == "unavailable" {
				_ = e.Store.Close()
			} else {
				if err := e.Store.Put(ctx, "directions", "broken", "not a direction", 0); err != nil {
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
	batch[0].Description = strings.Repeat("word ", 36)
	fixture := &ideationFixture{batch: batch}
	e.Adapter = func(model.Provider) provider.Adapter { return fixture }
	project, _ := e.Project(ctx, task.ProjectID)
	if err := e.Ideate(ctx, project, "2026-10-14"); err == nil || !strings.Contains(err.Error(), "35 words") {
		t.Fatalf("overlong ideas accepted: %v", err)
	}
	if fixture.calls != 3 {
		t.Fatalf("retries=%d", fixture.calls)
	}
	directions, _ := e.Store.List(ctx, "directions")
	if len(directions) != 0 {
		t.Fatal("rejected batch persisted")
	}
}
