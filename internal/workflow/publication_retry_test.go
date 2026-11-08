package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestNativePublicationMutationRequiresHostReconciliationBeforeRetry(t *testing.T) {
	e, task, project, record := acceptedDeliveryFixture(t)
	project.Policy.Commands = [][]string{{"gh"}}
	attempt := &remotePublicationPermissionAttempt{}
	e.Adapter = func(model.Provider) provider.Adapter { return attempt }
	step := &taskStep{Engine: e, ctx: context.Background(), project: project, task: task, record: record}
	if !runLocalDeliveryToCompletion(t, step) {
		t.Fatal("accepted candidate did not complete local delivery")
	}
	if !attempt.called || attempt.allowed {
		t.Fatalf("finalizer remote mutation permission was not denied: called=%v allowed=%v", attempt.called, attempt.allowed)
	}
	if step.task.PR != "" {
		t.Fatalf("local delivery recorded remote PR %q", step.task.PR)
	}
	publications, err := e.Store.List(context.Background(), "publications")
	if err != nil || len(publications) != 0 {
		t.Fatalf("local finalization wrote remote publication records: count=%d err=%v", len(publications), err)
	}
}

type remotePublicationPermissionAttempt struct {
	called  bool
	allowed bool
}

func (attempt *remotePublicationPermissionAttempt) Run(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
	if input.Role != "finalizer" || input.Purpose != "local_delivery_summary" || !input.ReadOnly {
		return provider.Output{}, store.ErrConflict
	}
	attempt.called = true
	allowed, err := hooks.Permission(ctx, provider.Permission{Command: []string{"gh", "pr", "create"}})
	attempt.allowed = allowed
	if err != nil {
		return provider.Output{}, err
	}
	return provider.Output{Text: "Reviewed candidate is ready for local handoff.", AttemptID: "finalizer-permission-attempt"}, nil
}
