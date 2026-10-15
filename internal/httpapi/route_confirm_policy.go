package httpapi

import (
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

func (q apiRequest) confirmPolicy() error {
	p, record, e := readCurrent[model.Project](q, "projects")
	if e != nil {
		return e
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
	p.Policy = updated
	p.PolicyHash = h
	q.save(q.w, q.r, "projects", q.id, p, record.Version, 200)
	return nil
}
