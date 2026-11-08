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

type quotaWindowData struct {
	Used     *float64 `json:"usedPercent"`
	Resets   *int64   `json:"resetsAt"`
	Duration *int64   `json:"windowDurationMins"`
}

type quotaLimitData struct {
	Primary   *quotaWindowData `json:"primary"`
	Secondary *quotaWindowData `json:"secondary"`
}

// ParseQuotaSnapshot retains reported windows, including unknown utilization.
func ParseQuotaSnapshot(data any, pool, source string, observed time.Time) (*model.QuotaSnapshot, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var v struct {
		Limits map[string]quotaLimitData `json:"rateLimitsByLimitId"`
		Limit  *quotaLimitData           `json:"rateLimits"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if len(v.Limits) == 0 && v.Limit != nil {
		v.Limits = map[string]quotaLimitData{"default": *v.Limit}
	}
	result := &model.QuotaSnapshot{RawMetadata: append(json.RawMessage(nil), b...), Applicability: "unknown", Pool: pool, Source: source, ObservedAt: observed.UTC().Format(time.RFC3339Nano), Windows: []model.QuotaWindow{}}
	if err := appendQuotaWindows(result, v.Limits); err != nil {
		return nil, err
	}
	return result, nil
}

func appendQuotaWindows(result *model.QuotaSnapshot, limits map[string]quotaLimitData) error {
	keys := make([]string, 0, len(limits))
	for id := range limits {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		lim := limits[id]
		for _, item := range []struct {
			name  string
			value *quotaWindowData
		}{{"primary", lim.Primary}, {"secondary", lim.Secondary}} {
			if item.value == nil {
				continue
			}
			w := item.value
			if !validQuotaUtilization(w.Used) {
				return fmt.Errorf("invalid quota utilization for %s", id)
			}
			result.Windows = append(result.Windows, model.QuotaWindow{LimitID: id, Window: item.name, UsedPercent: w.Used, ResetsAt: w.Resets, WindowDurationMins: w.Duration})
		}
	}
	return nil
}

func validQuotaUtilization(used *float64) bool {
	if used == nil {
		return true
	}
	return finiteQuotaUtilization(*used) && *used >= 0 && *used <= 100
}

func finiteQuotaUtilization(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// ReadQuotaSnapshot reads metadata without creating a model turn.
func (a ProcessAdapter) ReadQuotaSnapshot(ctx context.Context, cwd string) (*model.QuotaSnapshot, error) {
	supervisorDir, err := defaultStandaloneSupervisorDir()
	if err != nil {
		return nil, err
	}
	return a.ReadQuotaSnapshotWithSupervisorDir(ctx, cwd, supervisorDir)
}

// ReadQuotaSnapshotWithSupervisorDir stores runtime recovery state alongside
// the owning supervisor without mounting that directory into the sandbox.
func (a ProcessAdapter) ReadQuotaSnapshotWithSupervisorDir(ctx context.Context, cwd, supervisorDir string) (*model.QuotaSnapshot, error) {
	data, err := a.readCodexQuotaDataWithSupervisorDir(ctx, cwd, supervisorDir)
	if err != nil {
		return nil, err
	}
	pool := a.Provider.QuotaPool
	if pool == "" {
		pool = a.Provider.ID
	}
	return ParseQuotaSnapshot(data, pool, "metadata", time.Now())
}
