package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const detailsLogType = "stype=run/details"

func TestRunLogDetailsReplayDoesNotDuplicateContext(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", persistedRunID, model.Attempt{Role: "code", Status: "running"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, persistedRunID, "output", persistedText, []byte("first"), 100, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	if _, err := restarted.Capture(ctx, persistedRunID, "output", persistedText, []byte("second"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Count(string(body), detailsLogType) != 1 || strings.Count(string(body), "\n") != 3 {
		t.Fatalf("restart duplicated context or lost raw lines: %q %v", body, err)
	}
}

func TestRunLogDetailsRepairPartialContextAfterRestart(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", persistedRunID, model.Attempt{Role: "code", Status: "running"}, 1); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, int64(len(before)-7)); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	if _, err := restarted.Capture(ctx, persistedRunID, "output", persistedText, []byte("after repair"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Count(string(body), detailsLogType) != 1 || strings.Count(string(body), "\n") != 2 || !strings.Contains(string(body), "after repair") {
		t.Fatalf("partial context did not recover: %q %v", body, err)
	}
}

func TestRunLogDetailsUpgradeLegacyFileOnce(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", persistedRunID, model.Attempt{Status: "failed", Error: "legacy error"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, persistedRunID, "output", persistedText, []byte("old output"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw strings.Builder
	for _, line := range strings.SplitAfter(string(body), "\n") {
		if !strings.Contains(line, detailsLogType) {
			raw.WriteString(line)
		}
	}
	if err := os.WriteFile(path, []byte(raw.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".details"); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	for range 2 {
		if _, err := reader.RunLogPath(ctx, persistedRunID); err != nil {
			t.Fatal(err)
		}
	}
	body, err = os.ReadFile(path)
	if err != nil || strings.Count(string(body), "legacy error") != 1 || !strings.HasPrefix(string(body), raw.String()) {
		t.Fatalf("legacy upgrade rewrote raw log or duplicated details: %q %v", body, err)
	}
}
