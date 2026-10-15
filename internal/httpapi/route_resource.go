package httpapi

import (
	"encoding/json"
	"fmt"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (q apiRequest) resource() error {
	kinds := map[string]bool{"quota_observations": true, "review_parts": true, "audit": true, "publications": true, "mutations": true, "projects": true, "providers": true, "directions": true, "tasks": true, "plans": true, "reviews": true, "findings": true, "permissions": true, "questions": true, "inbox": true, "metrics": true, "outcomes": true, "sessions": true, "checkpoints": true, "units": true, "indexes": true, "index_history": true, "verification": true, "operations": true}
	if !kinds[q.kind] || q.action != "" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	if q.r.Method == "GET" {
		return q.getResource()
	}
	if q.r.Method == "PATCH" && (q.kind == "providers" || q.kind == "inbox") {
		return q.patchResource()
	}
	return &apiError{404, "route_not_found", "Unsupported route"}
}
func (q apiRequest) getResource() error {
	if q.id == "" {
		q.list(q.w, q.r, q.kind)
		return nil
	}
	record, err := q.Store.Get(q.r.Context(), q.kind, q.id)
	if err != nil {
		return err
	}
	q.w.Header().Set("ETag", fmt.Sprintf("\"%d\"", record.Version))
	reply(q.w, 200, record)
	return nil
}
func (q apiRequest) patchResource() error {
	record, err := q.currentRecord(q.kind)
	if err != nil {
		return err
	}
	value, err := q.updatedResource(record)
	if err != nil {
		return err
	}
	q.save(q.w, q.r, q.kind, q.id, value, record.Version, 200)
	return nil
}
func (q apiRequest) updatedResource(record model.Record) (any, error) {
	if q.kind == "inbox" {
		old, _ := store.Decode[model.Notification](record)
		old.Resolved = true
		return old, nil
	}
	var p model.Provider
	if err := json.Unmarshal(q.body, &p); err != nil {
		return nil, err
	}
	old, _ := store.Decode[model.Provider](record)
	p.ID = q.id
	if p.Kind != old.Kind || p.Kind == "unavailable" || len(p.Command) == 0 {
		return nil, &apiError{422, "invalid_input", "Provider kind cannot change; command required"}
	}
	p.Available = provider.Available(p)
	p.Quota = "unknown"
	return p, nil
}
