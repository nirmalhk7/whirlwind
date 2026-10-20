package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

func verifyInitializedOnboardingProject(t *testing.T, f onboardingFixture, body []byte) {
	t.Helper()
	var record model.Record
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil || project.PolicyHash == "" || len(project.Policy.AllowedPaths) == 0 {
		t.Fatalf("onboarding left execution unconfigured: %+v %v", project, err)
	}
	f.verifyIdeation(t, project, "codex")
	if _, hash, err := policy.Load(f.repo); err != nil || hash != project.PolicyHash {
		t.Fatalf("execution policy not loadable: %s %v", hash, err)
	}
	for _, name := range []string{".qlty/qlty.toml", ".codegraph/codegraph.db"} {
		if _, err := os.Stat(filepath.Join(f.repo, name)); err != nil {
			t.Fatal("project tool not initialized:", err)
		}
	}
	if records, err := f.server.Store.List(f.ctx, "projects"); err != nil || len(records) != 1 {
		t.Fatalf("ideation project missing: %v %v", records, err)
	}
}

func assertBlockedOnboardingFiles(t *testing.T, f onboardingFixture) {
	t.Helper()
	for _, name := range []string{onboardingPolicyYML, ".qlty", ".codegraph"} {
		if _, err := os.Stat(filepath.Join(f.repo, name)); !os.IsNotExist(err) {
			t.Fatalf("blocked onboarding wrote %s: %v", name, err)
		}
	}
}

func configureStarterGitFailure(t *testing.T, f onboardingFixture, state string) {
	t.Helper()
	if state == "identity" {
		t.Setenv("GIT_AUTHOR_NAME", "")
	} else {
		head, err := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(f.repo, ".git", "MERGE_HEAD"), head+"\n", 0600)
	}
}

func prepareLegacyOnboardingPolicy(t *testing.T, f onboardingFixture, custom bool) string {
	t.Helper()
	legacy := "# Whirlwind starter policy: ideation only.\n# Configure checks, allowed_paths and risk_rules before enabling task execution.\nchecks: []\nallowed_paths: []\ncommands: []\nrisk_rules: []\n"
	if custom {
		legacy = "# User-owned policy\nchecks: []\nallowed_paths: []\ncommands: []\nrisk_rules: []\n"
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), legacy, 0600)
	writeFixtureFile(t, filepath.Join(f.repo, "package.json"), `{"packageManager":"npm@11.6.0","scripts":{"test":"vitest run","typecheck":"tsc --noEmit","build":"next build","lint":"eslint"}}`, 0600)
	for _, args := range [][]string{{"add", onboardingPolicyYML, "package.json"}, {"commit", "-m", "Legacy starter"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	project := model.Project{ID: "p", Name: "Project", Repo: "whirlwind-fixture/demo", Path: f.repo}
	if err := f.server.Store.Put(f.ctx, "projects", "p", project, 0); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func assertUpgradedOnboardingPolicy(t *testing.T, f onboardingFixture, body []byte) {
	t.Helper()
	var record model.Record
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Project](record)
	if err != nil || saved.PolicyHash == "" {
		t.Fatalf("policy still not ready: %s %v", body, err)
	}
	assertOnboardingChecks(t, saved.Policy.Checks)
	if _, _, err := policy.Load(f.repo); err != nil {
		t.Fatal(err)
	}
}

func assertOnboardingChecks(t *testing.T, checks []model.Check) {
	t.Helper()
	for _, name := range []string{"qlty", "test", "typecheck", "build", "lint"} {
		found := false
		for _, check := range checks {
			if check.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing check %s: %+v", name, checks)
		}
	}
}
