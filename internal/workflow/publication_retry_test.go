package workflow

import (
	"context"
	"errors"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestNativePublicationMutationRequiresHostReconciliationBeforeRetry(t *testing.T) {
	e, task, path := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, errors.New("connection reset by peer after remote write")
		})
	}
	_, err := e.agent(context.Background(), p, task, provider.Input{Role: "submit", Purpose: "integration:create_pr", CWD: path, Prompt: "create native PR"})
	if err == nil || calls != 1 {
		t.Fatalf("uncertain create retried without host lookup: calls=%d err=%v", calls, err)
	}
}
