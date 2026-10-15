package httpapi

import (
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (q apiRequest) createProvider() error {
	var p model.Provider
	if e := json.Unmarshal(q.body, &p); e != nil {
		return e
	}
	if p.ID == "" || p.Name == "" || len(p.Command) == 0 || p.Kind != "acp" {
		return &apiError{422, "invalid_input", "Custom provider needs id, name, kind=acp and command array"}
	}
	p.Available = provider.Available(p)
	p.Quota = "unknown"
	q.save(q.w, q.r, "providers", p.ID, p, 0, 201)
	return nil
}
