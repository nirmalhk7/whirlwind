// Package control exposes local supervisor commands and inspection state.
package control

import (
	"context"

	"whirlwind/internal/model"
)

type Snapshot map[string][]model.Record

type Records interface {
	List(context.Context, string) ([]model.Record, error)
}

func Inspect(ctx context.Context, records Records) (Snapshot, error) {
	snapshot := Snapshot{}
	for _, kind := range []string{"projects", "tasks", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plans", "agents", "providers", "project_status"} {
		values, err := records.List(ctx, kind)
		if err != nil {
			return nil, err
		}
		snapshot[kind] = values
	}
	return snapshot, nil
}
