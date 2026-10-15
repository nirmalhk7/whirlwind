package httpapi

import (
	"encoding/json"
	"whirlwind/internal/model"
)

func (q apiRequest) decidePermission() error {
	n, e := expected(q.r)
	if e != nil {
		return &apiError{428, "precondition_required", e.Error()}
	}
	record, e := q.Store.Get(q.r.Context(), "permissions", q.id)
	if e != nil {
		return e
	}
	var p map[string]any
	_ = json.Unmarshal(record.Data, &p)
	var input struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(q.body, &input)
	if err := validatePermissionDecision(p, input.Decision); err != nil {
		return err
	}
	p["decision"] = input.Decision
	task, _ := p["task"].(string)
	attempt, _ := p["attempt_id"].(string)
	if err := q.Store.Audit(q.r.Context(), model.Audit{Task: task, AttemptID: attempt, Source: "local_operator", Action: "permission_" + input.Decision, Target: q.id, Version: n}); err != nil {
		return err
	}
	q.save(q.w, q.r, "permissions", q.id, p, n, 200)
	return nil
}

func validatePermissionDecision(p map[string]any, decision string) error {
	if decision != "allow" && decision != "deny" {
		return &apiError{422, "invalid_input", "decision must be allow or deny"}
	}
	if p["decision"] != "pending" {
		return &apiError{409, "already_decided", "Permission already answered"}
	}
	return nil
}
