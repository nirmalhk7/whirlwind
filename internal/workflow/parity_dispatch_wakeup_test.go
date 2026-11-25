package workflow

import (
	"context"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

// A queued approved idea must be observed before the periodic two-second scan.
func TestParityPlannerScansAtStartup(t *testing.T) {
	e, _ := approvedIdeaPlannerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	started := make(chan struct{}, 1)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	go func() { e.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-started:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("startup left approved backlog waiting for periodic scan")
	}
}

func TestParityPlannerWakeScansCommittedBacklog(t *testing.T) {
	e, _ := approvedIdeaPlannerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	started := make(chan struct{}, 1)
	r, _ := e.Store.Get(ctx, "settings", "global")
	settings := e.settings(ctx)
	settings.Stopped = true
	if err := e.Store.Put(ctx, "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	go func() { e.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	// Let the stopped startup scan finish before committing eligible work.
	time.Sleep(100 * time.Millisecond)
	r, _ = e.Store.Get(ctx, "settings", "global")
	settings.Stopped = false
	if err := e.Store.Put(ctx, "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	if waker, ok := any(e).(interface{ Wake() }); ok {
		waker.Wake()
	}
	select {
	case <-started:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("committed wake hint did not dispatch backlog")
	}
}
