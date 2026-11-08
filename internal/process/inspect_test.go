package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedCleanupRetainsEvidenceAndCanRetry(t *testing.T) {
	root := t.TempDir()
	t.Setenv(registryEnvironment, root)
	cmd := Command(context.Background(), "true")
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700); _ = Forget(cmd) })
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	if err := Wait(cmd); !errors.Is(err, ErrCleanup) {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, token(cmd)+".json")); err != nil {
		t.Fatal("recovery evidence removed before verified cleanup")
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Forget(cmd); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(root)
	if err != nil || !report.Clean || len(report.Processes) != 0 {
		t.Fatalf("retry did not prove cleanup: %+v %v", report, err)
	}
}

func TestProcessOwnershipUsesExactEnvironmentEntries(t *testing.T) {
	expected := strings.Repeat("0123456789abcdef", 2)
	for _, environment := range [][]string{
		{"ARG=" + processTokenPrefix + expected},
		{processTokenPrefix + expected + "-suffix"},
		{"OTHER_" + processTokenPrefix + expected},
	} {
		found, _, err := processOwnership(environment)
		if err != nil || found != "" {
			t.Fatalf("non-owner matched: token=%q err=%v", found, err)
		}
	}
	found, _, err := processOwnership([]string{processTokenPrefix + expected})
	if err != nil || found != expected {
		t.Fatalf("exact ownership rejected: %q %v", found, err)
	}
}

func TestReusedPIDDoesNotAuthorizeAnotherProcessGroup(t *testing.T) {
	record := registration{PID: 2000001, Token: strings.Repeat("0123456789abcdef", 2), Started: "original"}
	processes := []inspectedProcess{{Identity: Identity{PID: 2000001, Group: 2000001, Started: "reused"}}, {Identity: Identity{PID: 2000002, Parent: 2000001, Group: 2000001, Started: "other"}}}
	if found := liveProcesses(record, processes); len(found) != 0 {
		t.Fatalf("PID reuse selected unrelated processes: %+v", found)
	}
}
