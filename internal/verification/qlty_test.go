package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
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
			fixture := openshell.Install(t)
			fixture.EnableCommandExecution()
			t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
			bin := t.TempDir()
			tool := fmt.Sprintf("#!/bin/sh\nprintf '%%s' '%s'\n", tc.report)
			if err := os.WriteFile(filepath.Join(bin, "qlty"), []byte(tool), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			profile, err := execution.ForPolicy(bin, true, "fixture", nil)
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

func TestQltyKeepsSARIFStdoutSeparateFromProgressStderr(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stdout     string
		exit       string
		wantPassed bool
		wantExit   int
	}{
		{name: "valid SARIF with progress", stdout: `{"version":"2.1.0","runs":[{"results":[]}]}`, exit: "0", wantPassed: true, wantExit: 0},
		{name: "invalid stdout remains invalid", stdout: "not SARIF", exit: "0", wantPassed: false, wantExit: 0},
		{name: "tool error exit preserved", stdout: `{"version":"2.1.0","runs":[{"results":[]}]}`, exit: "3", wantPassed: false, wantExit: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, passed := runQltyCaptureCase(t, tc)
			assertQltyCaptureResult(t, result, passed, tc)
		})
	}
}

func runQltyCaptureCase(t *testing.T, tc struct {
	name       string
	stdout     string
	exit       string
	wantPassed bool
	wantExit   int
}) (Result, bool) {
	t.Helper()
	tool := fmt.Sprintf("#!/bin/sh\nprintf '%%s' '%s'\nprintf 'Scanning repository...\\n' >&2\nexit %s\n", tc.stdout, tc.exit)
	report := runQltyScript(t, tool)
	if len(report.Results) != 1 {
		t.Fatalf("check result missing: report=%+v", report)
	}
	return report.Results[0], report.Passed
}

func runQltyScript(t *testing.T, tool string) Report {
	t.Helper()
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "qlty"), []byte(tool), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := execution.ForPolicy(root, true, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunObservedWithProfile(context.Background(), root, qltySmellsCommand(), profile, nil)
	if err != nil {
		t.Fatalf("check runner returned error: %v", err)
	}
	return report
}

func TestQltyBoundsProgressDiagnostics(t *testing.T) {
	tool := `#!/bin/sh
printf '%s' '{"version":"2.1.0","runs":[{"results":[]}]}'
head -c 1048577 /dev/zero | tr '\000' x >&2
`
	report := runQltyScript(t, tool)
	if len(report.Results) != 1 {
		t.Fatalf("check result missing: report=%+v", report)
	}
	result := report.Results[0]
	if result.Output != `{"version":"2.1.0","runs":[{"results":[]}]}` || len(result.Diagnostics) != 1024*1024 || !result.Truncated {
		t.Fatalf("Qlty stdout or bounded diagnostics mismatch: output=%q diagnostic_bytes=%d truncated=%v", result.Output, len(result.Diagnostics), result.Truncated)
	}
}

func qltySmellsCommand() []model.Check {
	return []model.Check{{Name: "qlty-smells", Command: []string{"qlty", "smells", "--all", "--sarif", "--no-upgrade-check"}}}
}

func assertQltyCaptureResult(t *testing.T, result Result, passed bool, tc struct {
	name       string
	stdout     string
	exit       string
	wantPassed bool
	wantExit   int
}) {
	t.Helper()
	if passed != tc.wantPassed || result.Passed != tc.wantPassed {
		t.Fatalf("passed=%v want=%v: %+v", passed, tc.wantPassed, result)
	}
	if result.Output != tc.stdout {
		t.Fatalf("stdout was mixed or altered: %q", result.Output)
	}
	if result.Diagnostics != "Scanning repository...\n" {
		t.Fatalf("stderr diagnostics missing or altered: %q", result.Diagnostics)
	}
	if result.ExitCode == nil || *result.ExitCode != tc.wantExit {
		t.Fatalf("exit code changed: got=%v want=%d", result.ExitCode, tc.wantExit)
	}
}
