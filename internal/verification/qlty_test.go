package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func TestQltySmellsCannotPassWithFindingsOrInvalidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		want         bool
	}{
		{"clean", `{"version":"2.1.0","runs":[{"results":[]}]}`, true},
		{"findings", `{"version":"2.1.0","runs":[{"results":[{"ruleId":"complexity","message":{"text":"Too complex"}}]}]}`, false},
		{"invalid", `not a report`, false},
		{"missing runs", `{"version":"2.1.0"}`, false},
		{"tool failed", `{"version":"2.1.0","runs":[{"results":[],"invocations":[{"executionSuccessful":false}]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "qlty"), []byte("#!/bin/sh\nprintf '%s' \"$QUALITY_REPORT\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			t.Setenv("QUALITY_REPORT", tc.report)
			profile, err := execution.ForPolicy(bin, true, "fixture", &model.SandboxPolicy{Environment: []string{"QUALITY_REPORT"}})
			if err != nil {
				t.Fatal(err)
			}
			report, err := RunObservedWithProfile(context.Background(), bin, []model.Check{{Name: "qlty-smells", Command: []string{"qlty", "smells", "--all", "--sarif", "--no-upgrade-check"}}}, profile, nil)
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed != tc.want {
				t.Fatalf("passed=%v want=%v: %+v", report.Passed, tc.want, report)
			}
		})
	}
}
