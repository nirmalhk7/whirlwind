package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestCompactFooterPreservesRunAndAgentRows(t *testing.T) {
	d := New(t.TempDir())
	screen := dashboardScreen(t, d)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Fixture"})},
		"providers": {
			record("a", model.Provider{ID: "a", Name: "Alpha"}),
			record("b", model.Provider{ID: "b", Name: "Bravo"}),
			record("c", model.Provider{ID: "c", Name: "Charlie"}),
		},
		"provider_status": {
			record("a", model.ProviderStatus{ID: "a", Status: "working"}),
			record("b", model.ProviderStatus{ID: "b", Status: "failed"}),
		},
		"sessions": {
			record("3", model.Attempt{AttemptID: "3", ProjectID: "p", Role: "code", Provider: "a", Model: "model-one", Status: "running"}),
			record("2", model.Attempt{AttemptID: "2", ProjectID: "p", Role: "plan", Provider: "b", Model: "model-two", Status: "complete"}),
			record("1", model.Attempt{AttemptID: "1", ProjectID: "p", Role: "review", Provider: "c", Model: "model-three", Status: "failed"}),
		},
	})
	d.ForceDraw()
	screen.SetSize(80, 24)
	d.ForceDraw()
	view := screenText(screen)
	for _, item := range []string{"model-one", "model-two", "Alpha · ■ up", "Bravo · ■ down", "unverified", "? all keys"} {
		if !strings.Contains(view, item) {
			t.Errorf("80×24 screen hid %q:\n%s", item, view)
		}
	}
	for _, rows := range []struct {
		name string
		read func() (int, int, int, int)
	}{
		{"Runs", d.runs.GetInnerRect},
		{"Agents", d.agents.GetInnerRect},
	} {
		if _, _, _, height := rows.read(); height < 4 {
			t.Errorf("80×24 screen reserved only %d inner rows for %s", height, rows.name)
		}
	}
	d.input(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	d.ForceDraw()
	if help := screenText(screen); !strings.Contains(help, "Keyboard instructions") || !strings.Contains(help, "x stop/resume runners") {
		t.Fatal("compact footer did not lead to complete keyboard help")
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	screen.SetSize(160, 50)
	d.ForceDraw()
	if view := screenText(screen); !strings.Contains(view, "x stop/resume runners") {
		t.Fatal("full footer did not return after enlarging the terminal")
	}
}
