package httpapi

import (
	"encoding/json"
	"slices"
	"time"
	"whirlwind/internal/evaluation"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) evaluationReport() error {
	report, err := evaluation.Load(q.r.Context(), q.Store)
	if err != nil {
		return err
	}
	reply(q.w, 200, report)
	return nil
}

func (q apiRequest) taskOutcome() error {
	task, record, err := readCurrent[model.Task](q, "tasks")
	if err != nil {
		return err
	}
	var input struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err = json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	if !slices.Contains([]string{"accepted", "rejected", "regression", "reverted"}, input.Decision) {
		return &apiError{422, "invalid_outcome", "decision must be accepted, rejected, regression, or reverted"}
	}
	outcome := map[string]any{"task": task.ID, "pr": task.PR, "human_decision": input.Decision, "reason": input.Reason, "source": "local_operator", "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	audit := model.Audit{Task: task.ID, Source: "local_operator", Action: "record_outcome", Target: task.ID, Version: record.Version, Reason: input.Reason}
	if err = q.Store.Write(q.r.Context(), []store.Change{{Kind: "outcomes", ID: store.ID(), Value: outcome}, {Kind: "audit", ID: store.ID(), Value: audit}}, nil); err != nil {
		return err
	}
	q.save(q.w, q.r, "tasks", task.ID, task, record.Version, 200)
	return nil
}
