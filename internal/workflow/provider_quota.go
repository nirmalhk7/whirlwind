package workflow

import (
	"context"
	"errors"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type quotaReader interface {
	ReadQuota(context.Context, string) (string, string, error)
}

// A saved exhaustion flag must not prevent the metadata read that can clear it.
func (a *agentRun) refreshBlockedQuota(record model.Record) (model.Provider, error) {
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		return p, err
	}
	if p.Kind != "codex" || !p.Available || p.Quota != "exhausted" {
		return p, nil
	}
	reader, ok := a.Adapter(p).(quotaReader)
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
