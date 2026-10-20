package httpapi

import (
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) runnerStatus() error {
	if q.id != "status" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	records, err := q.Store.List(q.r.Context(), "sessions")
	if err != nil {
		return err
	}
	processing := false
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return err
		}
		if attempt.Status == "running" && !attempt.Finished {
			processing = true
			break
		}
	}
	reply(q.w, 200, map[string]bool{"processing": processing})
	return nil
}
