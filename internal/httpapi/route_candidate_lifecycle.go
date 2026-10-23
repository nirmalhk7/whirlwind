package httpapi

import (
	"whirlwind/internal/model"
)

func (q apiRequest) candidateLifecycle() error {
	d, record, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return err
	}
	switch q.action {
	case "dismiss":
		d.Status = "dismissed"
	case "defer":
		d.Status = "deferred"
	case "reopen":
		d.Status = "proposed"
	default:
		return &apiError{404, "route_not_found", "Unknown candidate action"}
	}
	if d.Selected {
		if err = q.pauseDirectionTask(directionTask(d)); err != nil {
			return err
		}
	}
	d.Selected = false
	if err = q.Store.Put(q.r.Context(), "directions", d.ID, d, record.Version); err != nil {
		return err
	}
	saved, err := q.Store.Get(q.r.Context(), "directions", d.ID)
	if err != nil {
		return err
	}
	reply(q.w, 200, saved)
	return nil
}
