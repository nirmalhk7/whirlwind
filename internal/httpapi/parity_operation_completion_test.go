package httpapi

import (
	"context"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityOperationCompletionRetainsPinnedRunIdentity(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	initial := model.Operation{Kind: "ideation", Subject: "p", Stage: "running", Owner: "worker"}
	if err := s.Store.Put(ctx, "operations", "run", initial, 0); err != nil {
		t.Fatal(err)
	}
	pinned := initial
	pinned.RepositoryRevision, pinned.ConfigurationHash = "frozen-repository", "frozen-configuration"
	if err := s.Store.Put(ctx, "operations", "run", pinned, 1); err != nil {
		t.Fatal(err)
	}
	initial.Stage = "complete"
	(apiRequest{Server: s}).finishOperation("run", initial)
	r, err := s.Store.Get(ctx, "operations", "run")
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := store.Decode[model.Operation](r)
	if actual.Stage != "complete" || actual.RepositoryRevision != pinned.RepositoryRevision || actual.ConfigurationHash != pinned.ConfigurationHash {
		t.Fatalf("completion overwrote pinned run identity or failed: %+v", actual)
	}
}

func TestParityOperationCompletionCannotUndoHumanPause(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	initial := model.Operation{Kind: "ideation", Subject: "p", Stage: "running", Owner: "worker"}
	if err := s.Store.Put(ctx, "operations", "run", initial, 0); err != nil {
		t.Fatal(err)
	}
	paused := initial
	paused.Stage, paused.Error = "paused", model.GlobalRunnerStopReason
	if err := s.Store.Put(ctx, "operations", "run", paused, 1); err != nil {
		t.Fatal(err)
	}
	initial.Stage = "complete"
	(apiRequest{Server: s}).finishOperation("run", initial)
	r, err := s.Store.Get(ctx, "operations", "run")
	if err != nil || r.Version != 2 {
		t.Fatalf("late result overrode human pause: %+v %v", r, err)
	}
}
