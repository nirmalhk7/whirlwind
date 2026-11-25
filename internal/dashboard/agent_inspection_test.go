package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

const (
	inspectionFixtureTimestampLayout = "2006-01-02 15:04:05 MST"
)

func TestAgentInspectionSeparatesOrderedQueuesChecksAndOutcomes(t *testing.T) {
	d := New(t.TempDir())
	screen := dashboardScreen(t, d)
	screen.SetSize(80, 24)
	observed := time.Now().UTC().Truncate(time.Second).Add(-40 * time.Second)
	stale := observed.Add(-90 * time.Second)
	snapshot := control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Fixture"})},
		"providers": {
			record("sol", model.Provider{ID: "sol", Name: "Sol"}),
			record("cursor", model.Provider{ID: "cursor", Name: "Cursor"}),
			record("claude", model.Provider{ID: "claude", Name: "Claude"}),
		},
		"agents": {
			record("code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{
				{Provider: "sol", Model: "gpt-6.2-sol", Thinking: &model.ThinkingChoice{ConfigID: "reasoning_effort", Value: "high"}},
			}}),
			record("plan", model.AgentConfig{Role: "plan", Chain: []model.AgentChoice{
				{Provider: "sol", Model: "gpt-6.2-sol", Thinking: &model.ThinkingChoice{ConfigID: "reasoning_effort", Value: "medium"}},
				{Provider: "cursor", Model: "cursor-model"},
			}}),
		},
		"provider_status": {
			record("sol", workflow.ProviderStatus{ID: "sol", Status: "working", CheckedAt: observed.Format(time.RFC3339)}),
			record("cursor", workflow.ProviderStatus{ID: "cursor", Status: "failed", CheckedAt: stale.Format(time.RFC3339)}),
			record("claude", workflow.ProviderStatus{ID: "claude", Status: "paused", CheckedAt: observed.Format(time.RFC3339)}),
		},
		"settings": {record("global", workflow.Settings{ProviderStatusCheckSeconds: 60})},
		"sessions": {
			record("latest", model.Attempt{ProjectID: "p", Provider: "sol", Role: "plan", Status: "failed", StartedAt: observed.Format(time.RFC3339)}),
			record("older", model.Attempt{ProjectID: "p", Provider: "sol", Role: "ideate", Status: "complete", StartedAt: stale.Format(time.RFC3339)}),
			record("other", model.Attempt{ProjectID: "other", Provider: "sol", Role: "review", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}),
		},
	}
	d.Present(snapshot)
	d.input(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if d.focus != 4 {
		t.Fatalf("Tab did not focus Agents: %d", d.focus)
	}
	d.input(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	view, ok := d.modal.(*tview.TextView)
	if !ok {
		t.Fatal("Enter on Agents did not open queue and connection inspection")
	}
	report := view.GetText(false)
	for _, marker := range []string{
		"Configured role queues", "Planner:", "Coding:",
		"1. Sol / gpt-6.2-sol / reasoning: medium (reasoning_effort)",
		"2. Cursor / cursor-model / reasoning: provider default",
		"1. Sol / gpt-6.2-sol / reasoning: high (reasoning_effort)",
		"Connection checks (not run outcomes)",
		"Sol: up · checked " + observed.Format(inspectionFixtureTimestampLayout) + " (current)",
		"Cursor: down · checked " + stale.Format(inspectionFixtureTimestampLayout) + " (stale)",
		"Claude: paused · no completed check",
		"Latest attempt outcomes (selected project)", "Sol: Planner failed",
	} {
		if !strings.Contains(report, marker) {
			t.Errorf("inspection omitted %q:\n%s", marker, report)
		}
	}
	if strings.Index(report, "1. Sol") > strings.Index(report, "2. Cursor") || strings.Index(report, "Planner:") > strings.Index(report, "Coding:") {
		t.Fatal("display changed role or provider choice order")
	}
	if strings.Contains(report, "Reviewer completed") || strings.Contains(report, "Ideation completed") {
		t.Fatal("inspection misattributed an old or another project's attempt")
	}
	view.ScrollTo(8, 0)
	snapshot["provider_status"][0] = record("sol", workflow.ProviderStatus{ID: "sol", Status: "failed", CheckedAt: stale.Format(time.RFC3339)})
	d.Present(snapshot)
	if d.modal != view || !strings.Contains(view.GetText(false), "Sol: down · checked "+stale.Format(inspectionFixtureTimestampLayout)+" (stale)") {
		t.Fatal("the open view did not refresh the connection observation")
	}
	if row, _ := view.GetScrollOffset(); row != 8 {
		t.Fatalf("refresh lost scroll position: %d", row)
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if d.modal != nil || d.GetFocus() != d.agents {
		t.Fatal("Esc did not return to the Agents pane")
	}
}
