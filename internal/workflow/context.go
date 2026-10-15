package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// jsonText serializes internal context to JSON text for agent prompts, ignoring marshal errors.
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

// latestReview returns the most recently updated review payload for a task and plan version, or an empty JSON array.
func (e *Engine) latestReview(ctx context.Context, id string) string {
	rs, _ := e.Store.List(ctx, "reviews")
	value := "[]"
	for _, r := range rs {
		if strings.HasPrefix(r.ID, id+":") {
			value = string(r.Data)
		}
	}
	return value
}

// index refreshes the project index against its current base commit and persists changed snapshots.
func (e *Engine) index(ctx context.Context, p model.Project) (ctxengine.Index, error) {
	r, err := e.Store.Get(ctx, "indexes", p.ID)
	var old *ctxengine.Index
	if err == nil {
		x, decode := store.Decode[ctxengine.Index](r)
		if decode != nil {
			return x, decode
		}
		old = &x
	}
	base, err := e.base(ctx, p)
	if err != nil {
		return ctxengine.Index{}, err
	}
	idx, err := ctxengine.Build(ctx, p.Path, old, base)
	if err != nil {
		return idx, err
	}
	if old == nil || idx.Commit != old.Commit {
		if err = e.Store.Put(ctx, "indexes", p.ID, idx, r.Version); err != nil && !errors.Is(err, store.ErrConflict) {
			return idx, err
		}
	}
	return idx, nil
}
