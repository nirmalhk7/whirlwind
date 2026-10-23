package workflow

import (
	"context"
	"errors"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type quotaSnapshotReader interface {
	ReadQuotaSnapshot(context.Context, string) (*model.QuotaSnapshot, error)
}

type quotaReader interface {
	ReadQuota(context.Context, string) (string, string, error)
}

// A saved exhaustion flag must not prevent the metadata read that can clear it.
func (a *agentRun) refreshBlockedQuota(record model.Record) (model.Provider, error) {
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		return p, err
	}
	if p.Kind != "codex" || !p.Available {
		return p, nil
	}
	adapter := a.Adapter(p)
	if reader, ok := adapter.(quotaSnapshotReader); ok {
		_, gateErr := a.quotaAdmission(p)
		if p.Quota == "exhausted" || gateErr != nil {
			return a.refreshSnapshot(record, p, reader)
		}
	}
	if p.Quota != "exhausted" {
		return p, nil
	}
	reader, ok := adapter.(quotaReader)
	if !ok {
		return p, nil
	}
	return a.refreshQuota(record, p, reader)
}

func (a *agentRun) refreshQuota(record model.Record, p model.Provider, reader quotaReader) (model.Provider, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	state, reset, err := reader.ReadQuota(ctx, a.input.CWD)
	if err != nil {
		return p, nil
	}
	if state != "available" && state != "exhausted" {
		return p, nil
	}
	p.Quota, p.ResetAt = state, reset
	observation := map[string]any{"provider": p.ID, "quota": state, "reset_at": reset, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "source": "routing_refresh"}
	err = a.Store.Write(a.ctx, []store.Change{{Kind: "providers", ID: p.ID, Value: p, Expected: record.Version}, {Kind: "quota_observations", ID: store.ID(), Value: observation}}, nil)
	if errors.Is(err, store.ErrConflict) {
		return a.latestProvider(p.ID)
	}
	return p, err
}

func (a *agentRun) latestProvider(id string) (model.Provider, error) {
	// Another request updated quota or provider setup while this read was in flight.
	latest, err := a.Store.Get(a.ctx, "providers", id)
	if err != nil {
		return model.Provider{}, err
	}
	return store.Decode[model.Provider](latest)
}

func (a *agentRun) refreshSnapshot(record model.Record, p model.Provider, reader quotaSnapshotReader) (model.Provider, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	snapshot, err := reader.ReadQuotaSnapshot(ctx, a.input.CWD)
	if err != nil || snapshot == nil {
		return p, nil
	}
	snapshot.Pool = providerPool(p)
	p.QuotaSnapshot = snapshot
	p.Quota, p.ResetAt = quotaSnapshotSummary(snapshot)
	old, getErr := a.Store.Get(a.ctx, "quota_snapshots", snapshot.Pool)
	if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
		return p, getErr
	}
	observation := map[string]any{"provider": p.ID, "quota": p.Quota, "snapshot": snapshot, "observed_at": snapshot.ObservedAt, "source": "routing_refresh"}
	err = a.Store.Write(a.ctx, []store.Change{{Kind: "providers", ID: p.ID, Value: p, Expected: record.Version}, {Kind: "quota_snapshots", ID: snapshot.Pool, Value: snapshot, Expected: old.Version}, {Kind: "quota_observations", ID: store.ID(), Value: observation}}, nil)
	if errors.Is(err, store.ErrConflict) {
		return a.latestProvider(p.ID)
	}
	return p, err
}
func quotaSnapshotSummary(snapshot *model.QuotaSnapshot) (string, string) {
	state := "unknown"
	var reset int64
	unknownReset := false
	for _, w := range snapshot.Windows {
		if w.UsedPercent == nil {
			continue
		}
		if state == "unknown" {
			state = "available"
		}
		if *w.UsedPercent >= 100 {
			state = "exhausted"
			if w.ResetsAt == nil || *w.ResetsAt <= 0 {
				unknownReset = true
			} else if *w.ResetsAt > reset {
				reset = *w.ResetsAt
			}
		}
	}
	if reset > 0 && !unknownReset {
		return state, time.Unix(reset, 0).UTC().Format(time.RFC3339)
	}
	return state, ""
}
