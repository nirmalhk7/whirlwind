package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func TestConfiguredAgentsRenderRegistryAndConnectionColors(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(180, 50)
	d.Present(control.Snapshot{
		"providers": {
			record("online", model.Provider{ID: "online", Name: "Online Agent", Kind: "acp"}),
			record("offline", model.Provider{ID: "offline", Name: "Offline Agent", Kind: "claude"}),
			record("unused", model.Provider{ID: "unused", Name: "Unused Agent", Kind: "codex"}),
		},
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
	for _, want := range []string{" Agents ", "Online Agent", "Offline Agent", "Unused Agent", "up", "down", "unverified"} {
		if !strings.Contains(view, want) {
			t.Errorf("configured agent information missing %q", want)
		}
	}
	for _, unwanted := range []string{"code ·", "model-one", "model-two", "Reasoning:"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("role configuration leaked into Agents: %q", unwanted)
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

func TestAgentsPanelDoesNotTreatRoleQueuesAsAgents(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"agents": {record("code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "removed", Model: "old-model"}}})}})
	if got := d.agents.GetText(true); got != "No configured agents" {
		t.Fatalf("roles rendered as agents: %s", got)
	}
}

func TestAgentHeadingShowsProviderNameOnce(t *testing.T) {
	for _, name := range []string{"Codex", ""} {
		t.Run(name, func(t *testing.T) {
			d := New()
			d.Present(control.Snapshot{
				"providers":       {record("codex", model.Provider{ID: "codex", Name: name, Kind: "codex"})},
				"provider_status": {record("codex", workflow.ProviderStatus{ID: "codex", Status: "working"})},
			})
			text := d.agents.GetText(true)
			if strings.Count(strings.ToLower(text), "codex") != 1 {
				t.Fatalf("agent name repeated: %q", text)
			}
			if !strings.Contains(text, "up") {
				t.Fatalf("connection status missing: %q", text)
			}
		})
	}
}
