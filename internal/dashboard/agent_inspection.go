package dashboard

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func (d *Dashboard) showAgentInspection() {
	view := tview.NewTextView().SetScrollable(true)
	view.SetBorder(true).SetTitle(" Agent queues and connections · Esc to close ")
	d.modal, d.agentInspection = view, view
	d.refreshAgentInspection()
	d.SetRoot(view, true).SetFocus(view)
}

func (d *Dashboard) refreshAgentInspection() {
	view := d.agentInspection
	if view == nil {
		return
	}
	providers, providerErr := decodeRecords[model.Provider](d.snapshot["providers"])
	sort.SliceStable(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	names := make(map[string]string, len(providers))
	for _, provider := range providers {
		names[provider.ID] = providerDisplayName(provider)
	}
	var text strings.Builder
	writeRoleQueues(&text, d.snapshot, names)
	text.WriteString("\n")
	writeAgentConnectionChecks(&text, d.snapshot, providers, providerErr, time.Now())
	text.WriteString("\n")
	d.writeAgentAttemptOutcomes(&text, names)
	if report := text.String(); report != view.GetText(false) {
		row, column := view.GetScrollOffset()
		view.SetText(report).ScrollTo(row, column)
	}
}

func writeRoleQueues(text *strings.Builder, snapshot control.Snapshot, names map[string]string) {
	text.WriteString("Configured role queues\n")
	configs, err := decodeRecords[model.AgentConfig](snapshot["agents"])
	if err != nil {
		text.WriteString("Cannot read role queues: " + err.Error() + "\n")
		return
	}
	if len(configs) == 0 {
		text.WriteString("No configured role queues.\n")
		return
	}
	roles, byRole := orderedRoleQueues(configs)
	for _, role := range roles {
		if config, ok := byRole[role]; ok {
			writeRoleQueue(text, config, names)
		}
	}
}

func orderedRoleQueues(configs []model.AgentConfig) ([]string, map[string]model.AgentConfig) {
	roles := model.AgentRoles()
	byRole := make(map[string]model.AgentConfig, len(configs))
	var extra []string
	for _, config := range configs {
		byRole[config.Role] = config
		if !slices.Contains(roles, config.Role) {
			extra = append(extra, config.Role)
		}
	}
	sort.Strings(extra)
	return append(roles, extra...), byRole
}

func writeRoleQueue(text *strings.Builder, config model.AgentConfig, names map[string]string) {
	label := agentInspectionRoleLabel(config.Role)
	if config.InheritedFrom != "" {
		label += " (inherits " + agentInspectionRoleLabel(config.InheritedFrom) + ")"
	}
	fmt.Fprintf(text, "%s:\n", label)
	if len(config.Chain) == 0 {
		text.WriteString("  No configured choices.\n")
	}
	for i, choice := range config.Chain {
		provider, modelName, reasoning := roleQueueChoiceLabels(choice, names)
		fmt.Fprintf(text, "  %d. %s / %s / reasoning: %s\n", i+1, provider, modelName, reasoning)
	}
}

func roleQueueChoiceLabels(choice model.AgentChoice, names map[string]string) (provider, modelName, reasoning string) {
	const defaultLabel = "provider default"
	provider = names[choice.Provider]
	if provider == "" {
		provider = choice.Provider + " (not configured)"
	}
	modelName = choice.Model
	if modelName == "" {
		modelName = defaultLabel
	}
	reasoning = defaultLabel
	if choice.Thinking != nil && choice.Thinking.Value != "" {
		reasoning = choice.Thinking.Value
		if choice.Thinking.ConfigID != "" {
			reasoning += " (" + choice.Thinking.ConfigID + ")"
		}
	}
	return provider, modelName, reasoning
}

func agentInspectionRoleLabel(role string) string {
	if label := agentRoleLabel(role); label != "" {
		return label
	}
	return role
}

func writeAgentConnectionChecks(text *strings.Builder, snapshot control.Snapshot, providers []model.Provider, providerErr error, now time.Time) {
	text.WriteString("Connection checks (not run outcomes)\n")
	if providerErr != nil {
		text.WriteString("Cannot read configured providers: " + providerErr.Error() + "\n")
		return
	}
	if len(providers) == 0 {
		text.WriteString("No configured providers.\n")
		return
	}
	checks, err := decodeRecords[workflow.ProviderStatus](snapshot["provider_status"])
	if err != nil {
		text.WriteString("Cannot read connection checks: " + err.Error() + "\n")
		return
	}
	byProvider := make(map[string]workflow.ProviderStatus, len(checks))
	for _, check := range checks {
		byProvider[check.ID] = check
	}
	interval := agentCheckInterval(snapshot)
	for _, provider := range providers {
		fmt.Fprintf(text, "%s: %s\n", providerDisplayName(provider), describeAgentCheck(byProvider[provider.ID], now, interval))
	}
}

func agentCheckInterval(snapshot control.Snapshot) time.Duration {
	settings := workflow.DefaultSettings()
	for _, record := range snapshot["settings"] {
		if record.ID != "global" {
			continue
		}
		var saved workflow.Settings
		if json.Unmarshal(record.Data, &saved) == nil && saved.ProviderStatusCheckSeconds > 0 && saved.ProviderStatusCheckSeconds <= 86400 {
			settings.ProviderStatusCheckSeconds = saved.ProviderStatusCheckSeconds
		}
		break
	}
	return time.Duration(settings.ProviderStatusCheckSeconds) * time.Second
}

func describeAgentCheck(check workflow.ProviderStatus, now time.Time, interval time.Duration) string {
	label := "up"
	switch check.Status {
	case "working":
	case "failed":
		label = "down"
	case "paused":
		return "paused · no completed check"
	case "unavailable":
		return "unavailable · no completed check"
	default:
		return "unverified · no completed check"
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, check.CheckedAt)
	if err != nil {
		return label + " · check time not recorded"
	}
	freshness := "stale"
	if age := now.Sub(checkedAt); age >= 0 && age < interval {
		freshness = "current"
	}
	return fmt.Sprintf("%s · checked %s (%s)", label, checkedAt.UTC().Format("2006-01-02 15:04:05 MST"), freshness)
}

func (d *Dashboard) writeAgentAttemptOutcomes(text *strings.Builder, names map[string]string) {
	text.WriteString("Latest attempt outcomes (selected project)\n")
	if d.projectID == "" {
		text.WriteString("No project selected.\n")
		return
	}
	seen := map[string]bool{}
	for _, row := range d.runRows {
		attempt := row.attempt
		if row.operation != nil || attempt.Provider == "" || seen[attempt.Provider] {
			continue
		}
		seen[attempt.Provider] = true
		name := names[attempt.Provider]
		if name == "" {
			name = attempt.Provider
		}
		fmt.Fprintf(text, "%s: %s %s\n", name, agentInspectionRoleLabel(attempt.Role), runStatus(attempt))
	}
	if len(seen) == 0 {
		text.WriteString("No attempts recorded.\n")
	}
}
