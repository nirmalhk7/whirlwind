package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func TestConfiguredAgentsRenderModelsEffortAndConnectionColors(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(180, 50)
	d.Present(control.Snapshot{
		"agents": {
			record("code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{
				{Provider: "online", Model: "model-one", Thinking: &model.ThinkingChoice{ConfigID: "reasoning_effort", Value: "high"}},
				{Provider: "offline", Model: "model-two"},
			}}),
		},
		"provider_status": {
			record("online", workflow.ProviderStatus{ID: "online", Status: "working"}),
			record("offline", workflow.ProviderStatus{ID: "offline", Status: "failed"}),
		},
	})
	d.ForceDraw()
	view := screenText(screen)
	for _, want := range []string{" Agents ", "model-one", "high", "model-two", "default", "up", "down"} {
		if !strings.Contains(view, want) {
			t.Errorf("configured agent information missing %q", want)
		}
	}
	assertStatusColor(t, screen, "up", tcell.ColorGreen)
	assertStatusColor(t, screen, "down", tcell.ColorRed)
}

func assertStatusColor(t *testing.T, screen tcell.SimulationScreen, word string, want tcell.Color) {
	t.Helper()
	cells, _, _ := screen.GetContents()
	for i := 0; i+len(word) <= len(cells); i++ {
		var found strings.Builder
		for j := range len(word) {
			found.WriteString(string(cells[i+j].Runes))
		}
		if found.String() == word {
			foreground, _, _ := cells[i].Style.Decompose()
			if foreground == want {
				return
			}
		}
	}
	t.Errorf("%q lacks expected color %v", word, want)
}

func TestInboxKeepsDirectionApprovalsAccessible(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Project"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Review this direction", Status: "proposed"})},
	})
	// Tab from Projects visits Agents, Plans, then Inbox direction controls.
	for range 3 {
		d.input(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	}
	d.input(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	d.ForceDraw()
	if d.modal == nil || !strings.Contains(screenText(screen), "Review this direction") {
		t.Fatal("Inbox direction approval unavailable through keyboard navigation")
	}
}
