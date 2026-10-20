package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestProjectSetupCreatesCommittedExecutionPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	committed, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git("rm", onboardingPolicyYAML)
	git("-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Remove policy")
	body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 201)
	starter, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML))
	if err != nil {
		t.Fatal("starter policy not created:", err)
	}
	verifyInitializedOnboardingProject(t, f, body)
	// Retrying must preserve the draft and avoid reinitializing configured tools.
	bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	for _, tool := range []string{"qlty", "codegraph"} {
		script := "#!/bin/sh\nexit 1\n"
		if tool == "qlty" {
			script = "#!/bin/sh\n[ \"$*\" = 'config validate --no-upgrade-check' ] && exit 0\nexit 1\n"
		}
		writeFixtureFile(t, filepath.Join(bin, tool), script, 0700)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 409)
	if current, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML)); err != nil || !bytes.Equal(current, starter) {
		t.Fatal("starter overwritten on retry")
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), string(committed), 0600)
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 422)
	git("add", onboardingPolicyYML, ".qlty/qlty.toml")
	git("-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Configure project")
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 409)
}

func TestProjectSetupStopsOnToolFailure(t *testing.T) {
	for _, tool := range []string{"qlty", "codegraph"} {
		t.Run(tool, func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			writeFixtureFile(t, filepath.Join(bin, tool), "#!/bin/sh\nprintf 'initialization failed\\n' >&2\nexit 1\n", 0700)
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(body, []byte(tool)) {
				t.Fatalf("missing failed tool: %s", body)
			}
			if records, err := f.server.Store.List(f.ctx, "projects"); err != nil || len(records) != 0 {
				t.Fatalf("failed setup registered: %v %v", records, err)
			}
		})
	}
}

func TestProviderStartupPreservesExistingConfiguration(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	record, err := f.server.Store.Get(f.ctx, "providers", "codex")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		t.Fatal(err)
	}
	p.Command = []string{"custom-codex", "app-server"}
	p.Available = false
	if err := f.server.Store.Put(f.ctx, "providers", p.ID, p, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := initializeProviders(f.ctx, f.server.Store, "another-bridge"); err != nil {
		t.Fatal(err)
	}
	saved, err := f.server.Store.Get(f.ctx, "providers", "codex")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Decode[model.Provider](saved)
	if err != nil || current.Command[0] != "custom-codex" || current.Available || saved.Version != record.Version+1 {
		t.Fatalf("startup overwrote user configuration: %+v, %v", current, err)
	}
}
