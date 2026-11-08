package httpapi

import (
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) taskBudget() error {
	task, record, err := q.budgetTask()
	if err != nil {
		return err
	}
	if q.r.Method == "PATCH" {
		return q.updateTaskBudget(task, record)
	}
	report, err := q.Store.TaskUsage(q.r.Context(), task.ID)
	if err != nil {
		return err
	}
	effective, source := q.effectiveTaskBudget(task)
	reply(q.w, 200, map[string]any{"budget": task.Budget, "effective_budget": effective, "budget_source": source, "usage": report, "version": record.Version, "enforcement": "local admission and active-runtime cancellation; token/cost cancellation requires live provider measurements; external quota consumption is not controlled"})
	return nil
}

func (q apiRequest) budgetTask() (model.Task, model.Record, error) {
	if q.r.Method == "PATCH" {
		return readCurrent[model.Task](q, "tasks")
	}
	record, err := q.Store.Get(q.r.Context(), "tasks", q.id)
	if err != nil {
		return model.Task{}, record, err
	}
	task, err := store.Decode[model.Task](record)
	return task, record, err
}

func (q apiRequest) updateTaskBudget(task model.Task, record model.Record) error {
	var budget model.BudgetPolicy
	if err := json.Unmarshal(q.body, &budget); err != nil {
		return err
	}
	if err := budget.Validate(); err != nil {
		return &apiError{422, "invalid_budget", err.Error()}
	}
	task.Budget = &budget
	audit := model.Audit{Task: task.ID, Source: "local_operator", Action: "budget_update", Target: task.ID, Version: record.Version, Reason: "Task allowance updated; existing consumption retained"}
	if err := q.Store.Put(q.r.Context(), "audit", store.ID(), audit, 0); err != nil {
		return err
	}
	q.save(q.w, q.r, "tasks", task.ID, task, record.Version, 200)
	return nil
}

func (q apiRequest) effectiveTaskBudget(task model.Task) (*model.BudgetPolicy, string) {
	effective, source := task.Budget, "task"
	if effective == nil {
		project, err := q.Engine.Project(q.r.Context(), task.ProjectID)
		if err == nil {
			effective, source = project.Policy.Budget, "project_policy"
		}
	}
	if effective == nil {
		if settings, err := q.Store.Get(q.r.Context(), "settings", "global"); err == nil {
			var value struct {
				Admission model.AdmissionSettings `json:"admission"`
			}
			if json.Unmarshal(settings.Data, &value) == nil {
				effective, source = value.Admission.DefaultBudget, "default"
			}
		}
	}
	if effective == nil {
		source = "unconfigured"
	}
	return effective, source
}
func (q apiRequest) taskUsage() error {
	if _, err := q.Store.Get(q.r.Context(), "tasks", q.id); err != nil {
		return err
	}
	report, err := q.Store.TaskUsage(q.r.Context(), q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, report)
	return nil
}

func (q apiRequest) reconcileTaskBudget() error {
	task, record, err := readCurrent[model.Task](q, "tasks")
	if err != nil {
		return err
	}
	return q.reconcileTaskBudgetFor(task, record)
}

func (q apiRequest) reconcileTaskBudgetFor(task model.Task, record model.Record) error {
	var input struct {
		AttemptID       string       `json:"attempt_id"`
		Usage           *model.Usage `json:"usage"`
		ActiveRuntimeMS *int64       `json:"active_runtime_ms"`
		Terminated      bool         `json:"confirmed_terminated"`
	}
	if err := json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	if !input.Terminated {
		return &apiError{422, "confirmation_required", "Confirm the interrupted provider child has terminated before releasing its runner reservation"}
	}
	return q.commitTaskBudgetReconciliation(task, record, input.AttemptID, input.Usage, input.ActiveRuntimeMS)
}

func (q apiRequest) commitTaskBudgetReconciliation(task model.Task, record model.Record, attemptID string, usage *model.Usage, activeRuntimeMS *int64) error {
	changes, err := q.Store.ReconciliationChanges(q.r.Context(), task.ID, attemptID, usage, activeRuntimeMS)
	if err != nil {
		return &apiError{409, "reconciliation_required", err.Error()}
	}
	if err = q.Store.Write(q.r.Context(), changes, nil); err != nil {
		return err
	}
	audit := model.Audit{Task: task.ID, Source: "local_operator", Action: "budget_reconcile", Target: attemptID, Version: record.Version, Reason: "Confirmed child termination; unknown consumption remains reserved"}
	if err = q.Store.Put(q.r.Context(), "audit", store.ID(), audit, 0); err != nil {
		return err
	}
	q.save(q.w, q.r, "tasks", task.ID, task, record.Version, 200)
	return nil
}
