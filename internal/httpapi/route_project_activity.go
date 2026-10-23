package httpapi

import (
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) projectActivity() error {
	p, record, err := readCurrent[model.Project](q, "projects")
	if err != nil {
		return err
	}
	p.Paused = q.action == "pause"
	if err := q.Store.Put(q.r.Context(), "projects", q.id, p, record.Version); err != nil {
		return err
	}
	if p.Paused {
		store.AfterCommit(q.r.Context(), func() { q.Engine.StopProject(q.id) })
	}
	saved, err := q.Store.Get(q.r.Context(), "projects", q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, saved)
	return nil
}
