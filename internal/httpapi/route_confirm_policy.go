package httpapi

import (
	"net/http"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

func (q apiRequest) confirmPolicy() error {
	p, record, e := readCurrent[model.Project](q, "projects")
	if e != nil {
		return e
	}
	if p.ManagedPolicy {
		return &apiError{409, "managed_policy", "Use the local policy approval command for this project"}
	}
	old, e := generatedLegacyStarter(p.Path)
	if e != nil {
		return e
	}
	if old != "" {
		if e = upgradeGeneratedStarter(q.r.Context(), p.Path, old); e != nil {
			return e
		}
	}
	updated, h, e := policy.Load(p.Path)
	if e != nil {
		return e
	}
	ctx := q.r.Context()
	if e := q.Engine.ConfirmProjectPolicy(ctx, p, record.Version, updated, h); e != nil {
		return e
	}
	saved, e := q.Store.Get(ctx, "projects", q.id)
	if e != nil {
		return e
	}
	reply(q.w, http.StatusOK, saved)
	return nil
}
