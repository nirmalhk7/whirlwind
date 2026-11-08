package workflow

import (
	"context"
	"errors"
	"testing"
	"whirlwind/internal/provider"
)

func runLocalDeliveryToCompletion(t *testing.T, step *taskStep) bool {
	t.Helper()
	for range 4 {
		finished, err := step.submitting()
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			return true
		}
		if err := step.Store.Put(step.ctx, "tasks", step.task.ID, step.task, step.record.Version); err != nil {
			t.Fatal(err)
		}
		step.record.Version++
	}
	return false
}

type countingLocalDeliveryAdapter struct{ calls int }

func (adapter *countingLocalDeliveryAdapter) Run(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	adapter.calls++
	if input.Role != "finalizer" || input.Purpose != "local_delivery_summary" || !input.ReadOnly {
		return provider.Output{}, errors.New("unexpected local delivery agent request")
	}
	return provider.Output{Text: "Recovered reviewed candidate is ready locally.", AttemptID: "recovered-finalizer"}, nil
}
