package verification

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Catches resolving against the supervisor's PATH/cwd instead of the job's.
func TestResolveJobCommand(t *testing.T) {
	stage := t.TempDir()
	bin := filepath.Join(stage, "bin")
	ambient := t.TempDir()
	requireVerificationSuccess(t, os.Mkdir(bin, 0700))
	for _, path := range []string{filepath.Join(bin, "tool"), filepath.Join(stage, "tool"), filepath.Join(ambient, "tool")} {
		requireVerificationSuccess(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700))
	}
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(stage, "not-executable"), []byte("data"), 0600))
	t.Setenv("PATH", ambient)
	for _, tc := range []jobCommandResolutionCase{
		{name: "approved PATH", executable: "tool", path: bin, override: true, want: filepath.Join(bin, "tool")},
		{name: "inherited PATH", executable: "tool", want: filepath.Join(ambient, "tool")},
		{name: "relative PATH entry", executable: "tool", path: "bin", override: true, want: filepath.Join(bin, "tool")},
		{name: "current directory PATH entry", executable: "tool", path: ".", override: true, want: filepath.Join(stage, "tool")},
		{name: "relative executable", executable: "./tool", want: filepath.Join(stage, "tool")},
		{name: "nested relative executable", executable: "bin/tool", want: filepath.Join(bin, "tool")},
		{name: "absolute executable", executable: filepath.Join(bin, "tool"), path: ambient, override: true, want: filepath.Join(bin, "tool")},
		{name: "missing in approved PATH", executable: "tool", path: t.TempDir(), override: true, wantError: true},
		{name: "empty approved PATH", executable: "tool", override: true, wantError: true},
		{name: "missing relative executable", executable: "./missing", wantError: true},
		{name: "missing absolute executable", executable: filepath.Join(stage, "missing"), wantError: true},
		{name: "non-executable", executable: "./not-executable", wantError: true},
		{name: "directory", executable: "./bin", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertJobCommandResolution(t, stage, tc)
		})
	}
}

type jobCommandResolutionCase struct {
	name, executable, path, want string
	override, wantError          bool
}

func assertJobCommandResolution(t *testing.T, stage string, tc jobCommandResolutionCase) {
	t.Helper()
	arguments := []string{"--flag=value with spaces", "", "$(touch forbidden)", "*", "a\nb"}
	command := JobCommand{Command: append([]string{tc.executable}, arguments...)}
	if tc.override {
		command.Environment = map[string]string{"PATH": tc.path}
	}
	original := slices.Clone(command.Command)
	got, err := resolveJobCommand(stage, command)
	if tc.wantError {
		if err == nil {
			t.Fatalf("resolved unavailable executable: %q", got)
		}
		return
	}
	requireVerificationSuccess(t, err)
	want := append([]string{tc.want}, arguments...)
	if !slices.Equal(got, want) {
		t.Fatalf("resolved command = %q, want %q", got, want)
	}
	if !slices.Equal(command.Command, original) {
		t.Fatalf("approved command changed: %q", command.Command)
	}
	got[1] = "changed"
	if !slices.Equal(command.Command, original) {
		t.Fatal("resolved arguments alias the approved command")
	}
}

func TestResolveJobCommandRejectsEmptyCommand(t *testing.T) {
	if _, err := resolveJobCommand(t.TempDir(), JobCommand{}); err == nil {
		t.Fatal("empty command accepted")
	}
}
