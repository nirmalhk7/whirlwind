package store

import (
	"context"
	"database/sql"
	"whirlwind/internal/model"
)

func usageDelta(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) (*model.Usage, error) {
	if entry.Usage == nil {
		return nil, nil
	}
	if err := validateReportedUsage(entry.Usage); err != nil {
		return nil, err
	}
	if entry.Usage.Scope != "attempt" && entry.Usage.Scope != "session" {
		return nil, nil
	}
	u := *entry.Usage
	u.Scope = "attempt"
	if entry.Usage.Scope != "session" {
		return &u, nil
	}
	return sessionUsageDelta(ctx, tx, entry, &u)
}

func sessionUsageDelta(ctx context.Context, tx *sql.Tx, entry model.UsageEntry, usage *model.Usage) (*model.Usage, error) {
	usage.Raw = nil
	previous, err := previousSessionUsage(ctx, tx, entry)
	if err != nil {
		return nil, err
	}
	if previous == nil {
		if entry.Resumed || entry.SessionID == "" {
			return nil, nil
		}
		return usage, nil
	}
	applySessionUsageDelta(usage, previous)
	return usage, nil
}

func previousSessionUsage(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) (*model.Usage, error) {
	entries, err := txValues[model.UsageEntry](ctx, tx, "usage")
	if err != nil {
		return nil, err
	}
	var previous *model.Usage
	for _, old := range entries {
		if !sameUsageSession(old, entry) {
			continue
		}
		if old.Usage != nil && old.Usage.Scope == "session" {
			previous = old.Usage
		}
	}
	return previous, nil
}

func sameUsageSession(old model.UsageEntry, current model.UsageEntry) bool {
	return sameUsageProviderSession(old, current) && sameUsageModelTransport(old, current) && sameUsageAttemptProfile(old, current)
}

func sameUsageProviderSession(old model.UsageEntry, current model.UsageEntry) bool {
	return old.Provider == current.Provider && old.SessionID == current.SessionID && current.SessionID != ""
}

func sameUsageModelTransport(old model.UsageEntry, current model.UsageEntry) bool {
	return old.Model == current.Model && current.Model != "" && old.Transport == current.Transport
}

func sameUsageAttemptProfile(old model.UsageEntry, current model.UsageEntry) bool {
	return old.ExecutionProfile == current.ExecutionProfile && old.PlanVersion == current.PlanVersion
}

func applySessionUsageDelta(current *model.Usage, previous *model.Usage) {
	current.TotalTokens = deltaInt(current.TotalTokens, previous.TotalTokens)
	current.InputTokens = deltaInt(current.InputTokens, previous.InputTokens)
	current.OutputTokens = deltaInt(current.OutputTokens, previous.OutputTokens)
	current.CachedTokens = deltaInt(current.CachedTokens, previous.CachedTokens)
	current.CacheWriteTokens = deltaInt(current.CacheWriteTokens, previous.CacheWriteTokens)
	if current.Cost == nil || previous.Cost == nil || *current.Cost < *previous.Cost {
		current.Cost = nil
		return
	}
	cost := *current.Cost - *previous.Cost
	current.Cost = &cost
}
func deltaInt(current, prior *int64) *int64 {
	if current == nil || prior == nil || *current < *prior {
		return nil
	}
	v := *current - *prior
	return &v
}
