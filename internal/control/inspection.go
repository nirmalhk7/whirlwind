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
	for _, kind := range []string{"projects", "execution_plans", "ideas", "operations", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plan_versions", "agents", "providers", "project_status", "settings"} {
		values, err := records.List(ctx, kind)
		if err != nil {
			return nil, err
		}
		if kind == "operations" {
			if source, ok := records.(interface {
				IdeationProgress(context.Context, string) ([]model.IdeationDirectionProgress, error)
			}); ok {
				if err := enrichIdeationProgress(ctx, values, source.IdeationProgress); err != nil {
					return nil, err
				}
			}
		}
		snapshot[kind] = values
	}
	return snapshot, nil
}
