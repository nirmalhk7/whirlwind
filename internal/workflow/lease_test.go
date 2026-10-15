package workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/store"
)

type leaseFixture struct {
	*Engine
	ctx     context.Context
	lease   store.Lease
	checked chan struct{}
}

func TestLeaseRenewal(t *testing.T) {
	for _, scenario := range []string{"renew", "lost lease", "changed task"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			fixture := startLeaseFixture(t, scenario)
			if scenario == "renew" {
				assertLeaseRenewed(t, fixture)
			} else {
				assertLeaseCancelled(t, fixture)
			}
		})
	}
}
func startLeaseFixture(t *testing.T, scenario string) leaseFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	engine := New(s, nil, "")
	lease, err := s.Claim(ctx, "work", engine.owner, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "lost lease" {
		if err = s.Release(ctx, "work", engine.owner, lease.Fence); err != nil {
			t.Fatal(err)
		}
	}
	checked := make(chan struct{}, 1)
	engine.renewLease(ctx, cancel, "work", lease.Fence, func() bool { checked <- struct{}{}; return scenario != "changed task" })
	return leaseFixture{engine, ctx, lease, checked}
}
func assertLeaseRenewed(t *testing.T, f leaseFixture) {
	t.Helper()
	select {
	case <-f.checked:
	case <-time.After(20 * time.Second):
		t.Fatal("lease was not renewed")
	}
	var expires int64
	if err := f.Store.DB.QueryRow("SELECT expires FROM leases WHERE id='work'").Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if f.ctx.Err() != nil || expires <= f.lease.ExpiresAt {
		t.Fatal("live work lost its lease")
	}
}
func assertLeaseCancelled(t *testing.T, f leaseFixture) {
	t.Helper()
	select {
	case <-f.ctx.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("invalid work was not cancelled")
	}
}
