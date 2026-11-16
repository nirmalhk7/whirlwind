package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) dispatchQueuedIdeation(ctx context.Context) {
	records, err := e.Store.List(ctx, "operations")
	if err != nil {
		return
	}
	for _, record := range records {
		op, err := store.Decode[model.Operation](record)
		if err != nil || op.Kind != "ideation" || op.Stage != "queued" {
			continue
		}
		project, err := e.Project(ctx, op.Subject)
		if err != nil || project.Paused {
			continue
		}
		key := "operation:" + record.ID
		lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
		if err != nil {
			continue
		}
		e.background.Go(func() {
			defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
			if err := e.runQueuedIdeation(ctx, project, record, key, lease.Fence); err != nil && !errors.Is(err, store.ErrConflict) {
				log.Printf("ideation run %s: %v", record.ID, err)
			}
		})
	}
}

func (e *Engine) runQueuedIdeation(ctx context.Context, project model.Project, record model.Record, key string, fence int64) error {
	ctx = store.WithLeaseFence(ctx, key, store.Lease{Owner: e.owner, Fence: fence})
	current, err := e.Store.Get(ctx, "operations", record.ID)
	if err != nil {
		return err
	}
	if current.Version != record.Version {
		return store.ErrConflict
	}
	op, err := store.Decode[model.Operation](current)
	if err != nil {
		return err
	}
	if op.Stage != "queued" {
		return store.ErrConflict
	}
	op.Stage, op.Owner, op.Error, op.FinishedAt = "running", e.owner, "", ""
	if op.StartedAt == "" {
		op.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := e.Store.Put(ctx, "operations", record.ID, op, record.Version); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.renewLease(ctx, cancel, key, fence, nil)
	input, _ := json.Marshal(op.Input)
	var request struct {
		Day   string `json:"day"`
		Fresh bool   `json:"fresh"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return err
	}
	if request.Day == "" {
		request.Day = time.Now().UTC().Format("2006-01-02")
	}
	failure := e.Ideate(WithFreshDiscovery(WithOperation(ctx, record.ID), request.Fresh), project, request.Day)
	return e.finishQueuedIdeation(ctx, record.ID, failure)
}

func (e *Engine) finishQueuedIdeation(ctx context.Context, id string, failure error) error {
	latest, err := e.Store.Get(context.WithoutCancel(ctx), "operations", id)
	if err != nil {
		return err
	}
	op, err := store.Decode[model.Operation](latest)
	if err != nil {
		return err
	}
	if op.Owner != e.owner || op.Stage != "running" {
		return store.ErrConflict
	}
	op.Stage = "complete"
	if failure != nil {
		op.Stage, op.Error = "failed", failure.Error()
		var accepted int
		if err := e.Store.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT count(*) FROM ideas WHERE run_id=?", id).Scan(&accepted); err != nil {
			return err
		} else if accepted > 0 {
			op.Stage = "partial"
		}
		if errors.Is(failure, store.ErrLeased) {
			op.Stage, op.Error = "queued", ""
		}
		if errors.Is(failure, ErrHumanDecisionPending) {
			op.Stage = "paused"
		}
	}
	if op.Stage != "queued" {
		op.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return e.Store.Put(context.WithoutCancel(ctx), "operations", id, op, latest.Version)
}
