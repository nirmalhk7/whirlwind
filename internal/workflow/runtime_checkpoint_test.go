package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestRuntimeCheckpointSurvivesCrashBeforeNewAttempt(t *testing.T) {
	engine, task, cwd := testEngine(t)
	input := provider.Input{Role: "code", CWD: cwd, Prompt: "continue task"}
	run := &agentRun{Engine: engine, ctx: context.Background(), task: task, input: input}
	if err := run.rotateSession("retry transaction with new approach", true); err != nil {
		t.Fatal(err)
	}
	restarted := &agentRun{Engine: engine, ctx: context.Background(), task: task, input: input}
	saved := storedSession{Session: "discarded-session", Runtime: &model.RuntimeSnapshot{Generation: 0}}
	resume, err := restarted.prepareRuntimeResume(saved)
	if err != nil || resume {
		t.Fatalf("discarded session resumed: resume=%v err=%v", resume, err)
	}
	if !strings.Contains(restarted.input.Prompt, "retry transaction with new approach") {
		t.Fatal("durable checkpoint omitted after generation change")
	}
	if _, err := restarted.prepareRuntimeResume(saved); err != nil {
		t.Fatal(err)
	}
	if strings.Count(restarted.input.Prompt, "Recovery checkpoint:") != 1 {
		t.Fatal("same checkpoint appended repeatedly")
	}
}

func TestRuntimeProgressIncludesUntrackedFileContents(t *testing.T) {
	engine, task, cwd := testEngine(t)
	run := &agentRun{Engine: engine, ctx: context.Background(), task: task, input: provider.Input{Role: "code", CWD: cwd}}
	attempt := &agentAttempt{agentRun: run}
	path := filepath.Join(cwd, "new implementation.txt")
	if err := os.WriteFile(path, []byte("first approach"), 0600); err != nil {
		t.Fatal(err)
	}
	before := attempt.progressFingerprint()
	if err := os.WriteFile(path, []byte("other approach"), 0600); err != nil {
		t.Fatal(err)
	}
	after := attempt.progressFingerprint()
	if before == "" || after == "" || before == after {
		t.Fatal("changed untracked contents did not count as progress")
	}
}

func TestRuntimeProgressRejectsUntrackedEscapingSymlink(t *testing.T) {
	engine, task, cwd := testEngine(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside repository"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: engine, ctx: context.Background(), task: task, input: provider.Input{Role: "code", CWD: cwd}}
	attempt := &agentAttempt{agentRun: run}
	if attempt.progressFingerprint() != "" {
		t.Fatal("unsafe untracked source must disable stall detection")
	}
}
