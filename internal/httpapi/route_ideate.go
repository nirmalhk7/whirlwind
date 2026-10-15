package httpapi

import (
	"log"
	"time"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (q apiRequest) ideate() error {
	p, e := q.Engine.Project(q.r.Context(), q.id)
	if e != nil {
		return e
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
			err := q.Engine.IdeateWhenAvailable(workflow.WithOperation(q.Context, operation), p, day)
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
