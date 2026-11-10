package dashboard

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rivo/tview"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func (d *Dashboard) refreshAgents() {
	agents, err := decodeRecords[model.Provider](d.snapshot["providers"])
	if err != nil {
		d.agents.SetText("Cannot read configured agents: " + tview.Escape(err.Error()))
		return
	}
	statuses, err := decodeRecords[workflow.ProviderStatus](d.snapshot["provider_status"])
	if err != nil {
		d.agents.SetText("Cannot read connection status: " + tview.Escape(err.Error()))
		return
	}
	health := map[string]string{}
	for _, status := range statuses {
		health[status.ID] = status.Status
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	var text strings.Builder
	for _, agent := range agents {
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		fmt.Fprintf(&text, "%s · %s\n", tview.Escape(name), connectionLabel(health[agent.ID]))
	}
	if text.Len() == 0 {
		text.WriteString("No configured agents")
	}
	d.agents.SetText(text.String())
}

func connectionLabel(status string) string {
	switch status {
	case "working":
		return "[green]■ up[-]"
	case "failed", "unavailable":
		return "[red]■ down[-]"
	case "paused":
		return "[yellow]■ paused[-] (runners stopped)"
	default:
		return "[yellow]■ unverified[-]"
	}
}
