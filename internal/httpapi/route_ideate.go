package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (q apiRequest) ideate() error {
	var input struct {
		model.IdeationBrief
		Fresh bool `json:"fresh"`
	}
	if err := json.Unmarshal(q.body, &input); err != nil {
		return &apiError{422, "invalid_input", "Ideation brief must be a JSON object"}
	}
	brief := input.IdeationBrief
	brief.Goal = strings.TrimSpace(brief.Goal)
	brief.Constraints = strings.TrimSpace(brief.Constraints)
	brief.Priorities = strings.TrimSpace(brief.Priorities)
	if len(brief.Goal) > 4000 || len(brief.Constraints) > 8000 || len(brief.Priorities) > 4000 || brief.MaxCandidates < 0 || brief.MaxCandidates > 12 {
		return &apiError{422, "invalid_input", "Ideation goal or constraints too long"}
	}
	p, e := q.Engine.Project(q.r.Context(), q.id)
	if e != nil {
		return e
	}
	if p.Paused {
		return &apiError{409, "project_paused", workflow.ErrProjectPaused.Error()}
	}
	if brief.Goal != "" || brief.Constraints != "" || brief.Priorities != "" || brief.MaxCandidates > 0 {
		record, err := q.Store.Get(q.r.Context(), "ideation_briefs", p.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err = q.Store.Put(q.r.Context(), "ideation_briefs", p.ID, brief, record.Version); err != nil {
			return err
		}
	}
	day, e := q.Engine.IdeationDay(q.r.Context(), time.Now())
	if e != nil {
		return e
	}
	operation := store.ID()
	result := q.operation("ideation", p.ID, map[string]string{"project_id": p.ID, "day": day})
	if err := q.Store.Put(q.r.Context(), "operations", operation, result, 0); err != nil {
		return err
	}
	store.AfterCommit(q.r.Context(), func() {
		go func() {
			err := q.Engine.IdeateWhenAvailable(workflow.WithFreshDiscovery(workflow.WithOperation(q.Context, operation), input.Fresh), p, day)
			result.Stage = "complete"
			if err != nil {
				result.Stage = "failed"
				result.Error = err.Error()
				log.Printf("ideation operation %s: %v", operation, err)
			}
			result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			q.finishOperation(operation, result)
		}()
	})
	reply(q.w, 202, map[string]string{"operation_id": operation})
	return nil
}
