package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
)

func TestUnrelatedDeletedProviderDoesNotBlockResume(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	unrelated := model.Attempt{Task: "unrelated", Provider: "deleted", SessionID: "old"}
	if err := h.engine.Store.Put(ctx, "sessions", "unrelated", unrelated, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(ctx); err != nil {
		t.Fatalf("unrelated session prevented execution: %v", err)
	}
}

func TestResumeUsesCreationOrderInsteadOfLastUpdate(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	state := model.Attempt{Task: h.task.ID, Role: "code", Provider: "fake", CWD: h.cwd, PlanVersion: 1, SessionID: "older"}
	if err := h.engine.Store.Put(ctx, "sessions", "older", state, 0); err != nil {
		t.Fatal(err)
	}
	state.SessionID = "newer"
	if err := h.engine.Store.Put(ctx, "sessions", "newer", state, 0); err != nil {
		t.Fatal(err)
	}
	state.SessionID = "older"
	if err := h.engine.Store.Put(ctx, "sessions", "older", state, 1); err != nil {
		t.Fatal(err)
	}
	got, err := h.engine.resumeSession(ctx, h.task.ID, "code", "fake", h.cwd, 1)
	if err != nil || got != "newer" {
		t.Fatalf("resumed %q error=%v", got, err)
	}
}
