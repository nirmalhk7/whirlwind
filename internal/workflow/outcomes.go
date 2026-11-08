package workflow

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type providerMetric struct {
	Provider string `json:"provider"`
	Role     string `json:"role"`
	Success  bool   `json:"success"`
	Category string `json:"category"`
	Task     string `json:"task"`
}

// outcomes computes provider success rates for comparable roles and repository categories once at least five outcomes are available.
func (e *Engine) outcomes(ctx context.Context, role, category string) map[string]float64 {
	records, _ := e.Store.List(ctx, "metrics")
	decisions := e.humanDecisions(ctx)
	totals := map[string]float64{}
	wins := map[string]float64{}
	for _, record := range records {
		var metric providerMetric
		_ = json.Unmarshal(record.Data, &metric)
		if metric.Role != role || metric.Category != category {
			continue
		}
		success, include := e.metricOutcome(ctx, metric, decisions)
		if !include {
			continue
		}
		totals[metric.Provider]++
		if success {
			wins[metric.Provider]++
		}
	}
	rates := map[string]float64{}
	for provider, total := range totals {
		if total >= 5 {
			rates[provider] = wins[provider] / total
		}
	}
	return rates
}
func (e *Engine) humanDecisions(ctx context.Context) map[string]string {
	decisions := map[string]string{}
	records, _ := e.Store.List(ctx, "outcomes")
	for _, record := range records {
		var outcome struct {
			Task     string `json:"task"`
			Decision string `json:"human_decision"`
		}
		_ = json.Unmarshal(record.Data, &outcome)
		decisions[outcome.Task] = outcome.Decision
	}
	return decisions
}
func (e *Engine) metricOutcome(ctx context.Context, m providerMetric, decisions map[string]string) (bool, bool) {
	if m.Role != "code" && m.Role != "review" {
		return m.Success, true
	}
	record, err := e.Store.Get(ctx, "tasks", m.Task)
	if err != nil {
		return false, false
	}
	task, err := store.Decode[model.Task](record)
	if err != nil || (task.Stage != "pr_created" && task.Stage != "complete") {
		return false, false
	}
	return m.Success && task.ChecksPassed && task.Score > 80 && decisions[m.Task] != "closed", true
}
