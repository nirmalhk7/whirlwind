package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentAttempt) checkUsageEvent(ctx context.Context, kind string, b []byte) error {
	var usage *model.Usage
	switch kind {
	case "thread/tokenUsage/updated":
		var event struct {
			TokenUsage struct {
				Total struct {
					Input  *int64 `json:"inputTokens"`
					Output *int64 `json:"outputTokens"`
					Cached *int64 `json:"cachedInputTokens"`
					Total  *int64 `json:"totalTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		if err := json.Unmarshal(b, &event); err != nil {
			return err
		}
		v := event.TokenUsage.Total
		usage = &model.Usage{Raw: append(json.RawMessage(nil), b...), Source: "codex", Completeness: "partial", CachedInputSemantics: "included_in_input", Scope: "session", InputTokens: v.Input, OutputTokens: v.Output, CachedTokens: v.Cached, TotalTokens: v.Total}
	case "agent/event":
		var event struct {
			Usage *model.Usage `json:"usage"`
		}
		if err := json.Unmarshal(b, &event); err != nil {
			return err
		}
		usage = event.Usage
	}
	if usage != nil && usage.InputTokens != nil && usage.OutputTokens != nil && usage.TotalTokens != nil && usage.Source == "codex" {
		usage.Completeness = "reported_complete"
	}
	if usage == nil {
		return nil
	}
	a.state.Usage = usage
	budget, err := a.budgetPolicy()
	if err != nil {
		return err
	}
	entry := model.UsageEntry{Transport: a.state.Transport, ExecutionProfile: a.state.ExecutionProfile, PlanVersion: a.task.PlanVersion, AttemptID: a.id, TaskID: a.task.ID, Provider: a.chosen.ID, Model: a.state.ResolvedModel, SessionID: a.state.SessionID, Resumed: a.state.RequestedSessionID != "", Usage: usage}
	if entry.Model == "" {
		entry.Model = a.input.Model
	}
	return a.Store.CheckUsageLimit(ctx, entry, budget)
}
func (a *agentAttempt) checkLiveQuota(ctx context.Context, data any) error {
	if a.input.Purpose == "provider_test" {
		return nil
	}
	snapshot, err := provider.ParseQuotaSnapshot(data, providerPool(a.chosen), "stream", time.Now())
	if err != nil {
		return nil
	}
	record, err := a.Store.Get(ctx, "providers", a.chosen.ID)
	if err != nil {
		return err
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		return err
	}
	p.QuotaSnapshot = snapshot
	p.Quota, p.ResetAt = quotaSnapshotSummary(snapshot)
	old, getErr := a.Store.Get(ctx, "quota_snapshots", snapshot.Pool)
	if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
		return getErr
	}
	if err = a.Store.Write(ctx, []store.Change{{Kind: "providers", ID: p.ID, Value: p, Expected: record.Version}, {Kind: "quota_snapshots", ID: snapshot.Pool, Value: snapshot, Expected: old.Version}}, nil); err != nil {
		return err
	}
	_, err = a.quotaAdmission(p)
	return err
}
