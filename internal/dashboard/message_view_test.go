package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestInboxVShowsDirectionRationaleWithoutOpeningDecisionOrSubmitting(t *testing.T) {
	capture := decisionVim(t, "cancel", "")
	d := New()
	screen := dashboardScreen(t, d)
	called := false
	d.SetCommandHandler(func(_ context.Context, _ control.Request) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	})
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {record("idea", model.Direction{
			ID: "idea", ProjectID: "p", GroupID: "latest-group", Day: "2026-11-10", Title: "Improve onboarding", Status: "proposed",
			Description:     "First-time contributors miss the generator workflow.",
			Problem:         "New contributors miss the generated-file policy.",
			Mechanism:       "Add a generated-file check to the contributor workflow.",
			ExpectedBenefit: "Reduce review cycles for first-time changes.",
			Scope:           []string{"CONTRIBUTING.md", "internal/tools/generate.go"},
			EffortEstimate:  "2 files",
			Uncertainty:     []string{"Forked repositories may need different policy."},
			Evidence:        []model.EvidenceRef{{Kind: "test", Ref: "internal/tools/generate_test.go", Summary: "Current generator tests omit contributor guidance."}},
		})},
	})
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	d.ForceDraw()
	view := screenText(screen)
	for _, want := range []string{"Description", "First-time contributors miss the generator workflow.", "Problem", "New contributors miss the generated-file policy.", "Approach", "Add a generated-file check", "Benefit", "Reduce review cycles", "Scope", "CONTRIBUTING.md", "internal/tools/generate.go", "Effort", "2 files", "Uncertainty", "Forked repositories may need different policy.", "internal/tools/generate_test.go"} {
		if !strings.Contains(view, want) {
			t.Errorf("Inbox rationale view omitted %q:\n%s", want, view)
		}
	}
	if called {
		t.Fatal("viewing rationale submitted a direction decision")
	}
	if !strings.Contains(view, "a choose direction") {
		t.Error("fullscreen rationale view hides how to reach direction decisions")
	}
	d.input(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if _, ok := d.modal.(*tview.List); !ok {
		t.Errorf("a did not open the existing explicit direction chooser: %T", d.modal)
	}
	if called {
		t.Fatal("opening the direction chooser submitted a decision")
	}
	if _, err := os.Stat(capture); err == nil {
		t.Fatal("v opened the decision editor instead of the read-only rationale view")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
