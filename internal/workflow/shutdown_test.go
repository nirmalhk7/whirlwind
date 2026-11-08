package workflow

import (
	"context"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestEngineShutdownWaitsForStandaloneProviderCleanup(t *testing.T) {
	e, _, _ := testEngine(t)
	providerCtx, providerCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer providerCancel()
	started, completed := make(chan struct{}), make(chan struct{})
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			close(started)
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	go func() {
		defer close(completed)
		_, _ = e.TestProviderConnection(providerCtx, model.Provider{ID: "standalone"})
	}()
	select {
	case <-started:
	case <-providerCtx.Done():
		t.Fatal("provider fixture did not start")
	}
	runCtx, stop := context.WithCancel(context.Background())
	stop()
	e.Run(runCtx)
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("engine shutdown left standalone provider running")
	}
}
