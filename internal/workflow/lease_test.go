package workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"khan/internal/store"
)

// TestLeaseRenewal verifies long-running work retains ownership and stops when its lease or task version becomes invalid.
func TestLeaseRenewal(t *testing.T) {
	for _, scenario := range []string{"renew", "lost lease", "changed task"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := New(s, nil, "")
			lease, err := s.Claim(ctx, "work", e.owner, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "lost lease" {
				if err = s.Release(ctx, "work", e.owner, lease.Fence); err != nil {
					t.Fatal(err)
				}
			}
			checked := make(chan struct{}, 1)
			e.renewLease(ctx, cancel, "work", lease.Fence, func() bool {
				checked <- struct{}{}
				return scenario != "changed task"
			})
			if scenario == "renew" {
				select {
				case <-checked:
				case <-time.After(20 * time.Second):
					t.Fatal("lease was not renewed")
				}
				var expires int64
				if err = s.DB.QueryRow("SELECT expires FROM leases WHERE id='work'").Scan(&expires); err != nil {
					t.Fatal(err)
				}
				if ctx.Err() != nil || expires <= lease.ExpiresAt {
					t.Fatal("live work lost its lease")
				}
			} else {
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Second):
					t.Fatal("invalid work was not cancelled")
				}
			}
		})
	}
}
