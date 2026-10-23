package httpapi

import (
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) localPlanDocument() error {
	view, err := q.Engine.ReadLocalPlan(q.r.Context(), q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, view)
	return nil
}
func (q apiRequest) reviewLocalPlan() error {
	version, err := expected(q.r)
	if err != nil {
		return &apiError{428, "precondition_required", err.Error()}
	}
	var input struct{ Action, Hash, Feedback string }
	if err = json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	if err = q.Engine.ReviewLocalPlan(q.r.Context(), q.id, version, input.Action, input.Hash, input.Feedback); err != nil {
		return err
	}
	record, err := q.Store.Get(q.r.Context(), "tasks", q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, record)
	return nil
}
func (q apiRequest) integrationInventory() error {
	if _, err := q.Store.Get(q.r.Context(), "providers", q.id); err != nil {
		return err
	}
	record, err := q.Store.Get(q.r.Context(), "integrations", q.id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if q.r.Method == "GET" {
		if errors.Is(err, store.ErrNotFound) {
			reply(q.w, 200, map[string]any{"provider": q.id, "integrations": []any{}, "error": "Not checked; refresh to discover native integrations"})
		} else {
			reply(q.w, 200, json.RawMessage(record.Data))
		}
		return nil
	}
	return q.refreshIntegrationInventory(record.Version)
}

func (q apiRequest) refreshIntegrationInventory(version int) error {
	catalog, probeErr := q.Engine.IntegrationInventory(q.r.Context(), q.id)
	if catalog == nil {
		catalog = []model.Integration{}
	}
	value := map[string]any{"provider": q.id, "integrations": catalog, "checked_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if probeErr != nil {
		value["error"] = probeErr.Error()
	}
	safe, err := store.SafeJSON(value)
	if err != nil {
		return err
	}
	if err = q.Store.Put(q.r.Context(), "integrations", q.id, json.RawMessage(safe), version); err != nil {
		return err
	}
	reply(q.w, 200, json.RawMessage(safe))
	return nil
}
