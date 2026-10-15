package httpapi

import (
	"encoding/json"
	"errors"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (q apiRequest) getSettings() error {
	v, e := q.Store.Get(q.r.Context(), "settings", "global")
	if errors.Is(e, store.ErrNotFound) {
		reply(q.w, 200, map[string]any{"id": "global", "version": 0, "data": workflow.DefaultSettings()})
		return nil
	}
	if e != nil {
		return e
	}
	settings := workflow.DefaultSettings()
	if e = json.Unmarshal(v.Data, &settings); e != nil {
		return e
	}
	v.Data, e = json.Marshal(settings)
	if e != nil {
		return e
	}
	reply(q.w, 200, v)
	return nil
}
