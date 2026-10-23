package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
	"whirlwind/internal/model"
)

// ParseQuotaSnapshot retains reported windows, including unknown utilization.
func ParseQuotaSnapshot(data any, pool, source string, observed time.Time) (*model.QuotaSnapshot, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	type window struct {
		Used     *float64 `json:"usedPercent"`
		Resets   *int64   `json:"resetsAt"`
		Duration *int64   `json:"windowDurationMins"`
	}
	type limit struct {
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
	}
	var v struct {
		Limits map[string]limit `json:"rateLimitsByLimitId"`
		Limit  *limit           `json:"rateLimits"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if len(v.Limits) == 0 && v.Limit != nil {
		v.Limits = map[string]limit{"default": *v.Limit}
	}
	result := &model.QuotaSnapshot{RawMetadata: append(json.RawMessage(nil), b...), Applicability: "unknown", Pool: pool, Source: source, ObservedAt: observed.UTC().Format(time.RFC3339Nano), Windows: []model.QuotaWindow{}}
	keys := make([]string, 0, len(v.Limits))
	for id := range v.Limits {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		lim := v.Limits[id]
		for _, item := range []struct {
			name  string
			value *window
		}{{"primary", lim.Primary}, {"secondary", lim.Secondary}} {
			if item.value == nil {
				continue
			}
			w := item.value
			if w.Used != nil && (math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0 || *w.Used > 100) {
				return nil, fmt.Errorf("invalid quota utilization for %s", id)
			}
			result.Windows = append(result.Windows, model.QuotaWindow{LimitID: id, Window: item.name, UsedPercent: w.Used, ResetsAt: w.Resets, WindowDurationMins: w.Duration})
		}
	}
	return result, nil
}

// ReadQuotaSnapshot reads metadata without creating a model turn.
func (a ProcessAdapter) ReadQuotaSnapshot(ctx context.Context, cwd string) (*model.QuotaSnapshot, error) {
	data, err := a.readCodexQuotaData(ctx, cwd)
	if err != nil {
		return nil, err
	}
	pool := a.Provider.QuotaPool
	if pool == "" {
		pool = a.Provider.ID
	}
	return ParseQuotaSnapshot(data, pool, "metadata", time.Now())
}
