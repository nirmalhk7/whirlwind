package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
)

// Omitting the sixth session requirement would resume a legacy or broader-authority session.
func TestResumeRequiresExecutionProfile(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	state := model.Attempt{Task: h.task.ID, Role: "code", Provider: "fake", CWD: h.cwd, PlanVersion: 1, SessionID: "old", ExecutionProfile: "old-policy"}
	if err := h.engine.Store.Put(ctx, "sessions", "profile-old", state, 0); err != nil {
		t.Fatal(err)
	}
	got, err := h.engine.resumeSession(ctx, h.task.ID, "code", "fake", h.cwd, 1, "", "", "", "", "", "current-profile")
	if err != nil || got != "" {
		t.Fatalf("old authority resumed: %q %v", got, err)
	}
	state.SessionID = "matching"
	state.ExecutionProfile = "current-profile"
	if err := h.engine.Store.Put(ctx, "sessions", "profile-current", state, 0); err != nil {
		t.Fatal(err)
	}
	got, err = h.engine.resumeSession(ctx, h.task.ID, "code", "fake", h.cwd, 1, "", "", "", "", "", "current-profile")
	if err != nil || got != "matching" {
		t.Fatalf("current authority not resumed: %q %v", got, err)
	}
}
