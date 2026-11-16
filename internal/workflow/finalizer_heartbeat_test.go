package workflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func runtimeConflictHasReason(err error, reason string) bool {
	return errors.Is(err, store.ErrConflict) && strings.Contains(err.Error(), reason)
}

func TestFinalizerFreshSessionReadStillRejectsLostOwnership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.Attempt)
	}{
		{"generation replaced", func(a *model.Attempt) { a.ExecutionGeneration++ }},
		{"owner replaced", func(a *model.Attempt) { a.Owner = "replacement-owner" }},
		{"execution replaced", func(a *model.Attempt) { a.ExecutionID = "replacement-execution" }},
		{"finished", func(a *model.Attempt) { a.Finished = true }},
		{"expired", func(a *model.Attempt) { a.LeaseExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano) }},
	} {
		t.Run(tc.name, func(t *testing.T) { assertFinalizerOwnershipFence(t, tc.change) })
	}
}

func assertFinalizerOwnershipFence(t *testing.T, change func(*model.Attempt)) {
	t.Helper()
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "finalizer-ownership")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	row, err := e.Store.Get(ctx, "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	changed := storedAttempt(t, e, attempt.id)
	change(&changed)
	if err := e.Store.Put(ctx, "sessions", attempt.id, changed, row.Version); err != nil {
		t.Fatal(err)
	}
	err = attempt.withLiveFinalizerAttempt(ctx, func(row model.Record, _ model.Attempt) error {
		return e.Store.Write(ctx, []store.Change{store.VersionPrecondition("sessions", row.ID, row.Version), {Kind: "operations", ID: "forbidden-finalization", Value: model.Operation{Kind: "finalization", Stage: "complete"}}}, nil)
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("lost runtime owner was authorized: %v", err)
	}
	if _, err := e.Store.Get(ctx, "operations", "forbidden-finalization"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale owner wrote finalization state: %v", err)
	}
}

func TestFinalizationSurvivesHeartbeatDuringGitPreparation(t *testing.T) {
	e, plan, _, _ := acceptedParityDeliveryFixture(t)
	for range 2 {
		if err := e.Step(context.Background(), plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	marker, release := holdFinalizationGitCommit(t)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			if err := runNativeFinalizerPeer(ctx, in); err != nil {
				return provider.Output{}, err
			}
			_, err := h.FinalizeDelivery(ctx)
			return provider.Output{Text: "Exact local delivery ready."}, err
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan error, 1)
	go func() { defer close(done); done <- e.Step(ctx, plan.ID) }()
	defer func() { release(); cancel(); <-done }()
	waitForFinalizationCommit(t, ctx, marker)
	attempt := activeFinalizerAttempt(t, e, plan)
	if err := attempt.recordWorkerHeartbeat(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("live heartbeat failed during trusted Git preparation: %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("heartbeat invalidated an otherwise authorized finalization: %v", err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	if saved.Stage != "complete" || saved.Delivery.DeliveryCommit == "" {
		t.Fatal("authorized finalization did not publish and complete")
	}
}

func activeFinalizerAttempt(t *testing.T, e *Engine, plan model.ExecutionPlan) *agentAttempt {
	t.Helper()
	rows, err := e.Store.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		state, err := store.Decode[model.Attempt](row)
		if err != nil {
			t.Fatal(err)
		}
		if state.Task == plan.ID && state.Role == "finalizer" && state.Purpose == "local_finalization" && !state.Finished {
			return &agentAttempt{agentRun: &agentRun{Engine: e}, id: row.ID, state: state}
		}
	}
	t.Fatal("no running Finalizer at Git commit boundary")
	return nil
}

// The real Git command is held at hash-object; no Store behavior is mocked.
func holdFinalizationGitCommit(t *testing.T) (string, func()) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	marker, released := filepath.Join(root, "entered"), filepath.Join(root, "released")
	script := "#!/bin/sh\nif [ \"$2\" = hash-object ]; then\n: > " + nativeFixtureShellQuote(marker) + "\nwhile [ ! -f " + nativeFixtureShellQuote(released) + " ]; do /bin/sleep 0.01; done\nfi\nexec " + nativeFixtureShellQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker, func() {
		if err := os.WriteFile(released, nil, 0600); err != nil {
			t.Error(err)
		}
	}
}

func waitForFinalizationCommit(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("Finalizer did not reach trusted Git commit boundary")
		case <-ticker.C:
		}
	}
}
