package workflow

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type ProviderStatus struct {
	QuotaPool     string               `json:"quota_pool,omitempty"`
	QuotaSnapshot *model.QuotaSnapshot `json:"quota_snapshot,omitempty"`
	QuotaFresh    *bool                `json:"quota_fresh"`
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Kind          string               `json:"kind"`
	Status        string               `json:"status"`
	Detail        string               `json:"detail"`
	CheckedAt     string               `json:"checked_at,omitempty"`
}
type ProviderStatuses struct {
	Providers       []ProviderStatus `json:"providers"`
	IntervalSeconds int              `json:"interval_seconds"`
	Stopped         bool             `json:"stopped"`
}
type providerStatusCache struct {
	mu      sync.Mutex
	key     string
	expires time.Time
	value   ProviderStatuses
}

// ProviderStatuses shares bounded connection checks across Settings tabs. Configuration
// changes and the stop flag invalidate cached results immediately.
func (e *Engine) ProviderStatuses(ctx context.Context) (ProviderStatuses, error) {
	e.providerStatuses.mu.Lock()
	defer e.providerStatuses.mu.Unlock()
	settings := e.settings(ctx)
	records, err := e.Store.List(ctx, "providers")
	if err != nil {
		return ProviderStatuses{}, err
	}
	if settings.ProviderStatusCheckSeconds < 1 {
		settings.ProviderStatusCheckSeconds = 30
	}
	encoded, _ := json.Marshal(struct {
		Records  []model.Record
		Stopped  bool
		Interval int
	}{records, settings.Stopped, settings.ProviderStatusCheckSeconds})
	key := string(encoded)
	cache := &e.providerStatuses
	if cache.key == key && time.Now().Before(cache.expires) {
		return cache.value, nil
	}
	result := ProviderStatuses{Providers: make([]ProviderStatus, len(records)), IntervalSeconds: settings.ProviderStatusCheckSeconds, Stopped: settings.Stopped}
	providers, err := decodeStatusProviders(records)
	if err != nil {
		return result, err
	}
	var checks sync.WaitGroup
	for i, p := range providers {
		checks.Go(func() { result.Providers[i] = e.checkProviderStatus(ctx, p, settings.Stopped) })
	}
	checks.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return e.publishProviderStatuses(ctx, settings.Stopped, key, result)
}

func decodeStatusProviders(records []model.Record) ([]model.Provider, error) {
	providers := make([]model.Provider, len(records))
	for i, record := range records {
		p, err := store.Decode[model.Provider](record)
		if err != nil {
			return nil, err
		}
		providers[i] = p
	}
	return providers, nil
}

func (e *Engine) publishProviderStatuses(ctx context.Context, stopped bool, key string, result ProviderStatuses) (ProviderStatuses, error) {
	cache := &e.providerStatuses
	if latestStopped := e.settings(ctx).Stopped; latestStopped != stopped {
		cache.key = ""
		result.Stopped = latestStopped
		if !latestStopped {
			return result, nil
		}
		for i := range result.Providers {
			if result.Providers[i].Status != "unavailable" {
				result.Providers[i].Status = "paused"
				result.Providers[i].Detail = "Connection checks paused while runners are stopped."
			}
		}
		return result, nil
	}
	cache.key, cache.value, cache.expires = key, result, time.Now().Add(time.Duration(result.IntervalSeconds)*time.Second)
	return result, nil
}

func (e *Engine) checkProviderStatus(parent context.Context, p model.Provider, stopped bool) ProviderStatus {
	status := ProviderStatus{QuotaPool: providerPool(p), QuotaSnapshot: p.QuotaSnapshot, ID: p.ID, Name: p.Name, Kind: p.Kind, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if record, err := e.Store.Get(parent, "quota_snapshots", status.QuotaPool); err == nil {
		snapshot, decodeErr := store.Decode[model.QuotaSnapshot](record)
		if decodeErr == nil && (status.QuotaSnapshot == nil || snapshot.ObservedAt > status.QuotaSnapshot.ObservedAt) {
			status.QuotaSnapshot = &snapshot
		}
	}
	reserve, configured := e.settings(parent).Admission.Pools[status.QuotaPool]
	if !configured {
		reserve, configured = e.settings(parent).Admission.Pools["*"]
	}
	if configured && status.QuotaSnapshot != nil {
		observed, err := time.Parse(time.RFC3339Nano, status.QuotaSnapshot.ObservedAt)
		fresh := err == nil && time.Since(observed) >= 0 && time.Since(observed) <= time.Duration(reserve.MaxAgeSeconds)*time.Second
		status.QuotaFresh = &fresh
	}
	if !provider.Available(p) {
		status.Status = "unavailable"
		status.Detail = "Configured command executable not found."
		if p.Kind == "unavailable" {
			status.Detail = p.Limitation
			if status.Detail == "" {
				status.Detail = "Supported integration required."
			}
		}
		return status
	}
	if stopped {
		status.Status = "paused"
		status.Detail = "Command installed. Connection checks paused while runners are stopped."
		return status
	}
	ctx, release := e.runnerContext(parent)
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	capacity := model.Project{ID: "provider-status:" + p.ID}
	status.Status = "failed"
	if err := e.acquire(ctx, capacity); err != nil {
		status.Status = "unknown"
		status.Detail = "Check deferred while waiting for runner capacity."
		return status
	}
	defer e.release(capacity.ID)
	workspace, err := os.MkdirTemp("", "whirlwind-status-")
	if err == nil {
		defer func() { _ = os.RemoveAll(workspace) }()
		status.Detail, err = provider.CheckConnection(ctx, p, workspace)
	}
	if err != nil {
		status.Detail = err.Error()
	} else {
		status.Status = "working"
	}
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return status
}
