package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Losing the read retry would expose a temporary transport error instead of issues.
func TestIssueReadRecoversTransientConnection(t *testing.T) {
	installReadCommand(t, `if [ ! -f "$READ_STATE" ]; then touch "$READ_STATE"; echo 'read: operation timed out' >&2; exit 1; fi
printf '[{"number":17,"title":"Recovered","state":"OPEN"}]'`)
	issues, err := (CLI{}).Issues(context.Background(), "o/r")
	if err != nil || len(issues) != 1 || issues[0].Number != 17 {
		t.Fatalf("read did not recover: %+v %v", issues, err)
	}
}

// Retrying authentication failures would delay an actionable failure and run twice.
func TestIssueReadDoesNotRetryPermissionFailure(t *testing.T) {
	state := installReadCommand(t, `echo attempt >> "$READ_STATE"; echo 'Permission denied (publickey).' >&2; exit 1`)
	_, err := (CLI{}).Issues(context.Background(), "o/r")
	b, _ := os.ReadFile(state)
	if err == nil || string(b) != "attempt\n" {
		t.Fatalf("permission failure retried: %q %v", b, err)
	}
}

func TestIssueReadCancellationStopsRecovery(t *testing.T) {
	installReadCommand(t, `echo 'Connection reset by peer' >&2; exit 1`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (CLI{}).Issues(ctx, "o/r")
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("wrong cancellation: %v", err)
	}
}

func installReadCommand(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("READ_STATE", state)
	return state
}

func TestIssueReadStopsAfterRepeatedTransportFailures(t *testing.T) {
	state := installReadCommand(t, `echo attempt >> "$READ_STATE"; echo 'Connection closed by 172.182.252.133 port 22' >&2; exit 1`)
	_, err := (CLI{}).Issues(context.Background(), "o/r")
	b, _ := os.ReadFile(state)
	if err == nil || string(b) != "attempt\nattempt\nattempt\n" {
		t.Fatalf("unbounded or missing retries: %q %v", b, err)
	}
}
