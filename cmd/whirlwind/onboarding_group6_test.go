package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/store"
)

func TestOnboardingValidatesQltyAndVerifiesInitialization(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(fmt.Sprint("existing=", existing), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			if existing {
				if err := os.Mkdir(filepath.Join(f.repo, ".qlty"), 0700); err != nil {
					t.Fatal(err)
				}
				writeFixtureFile(t, filepath.Join(f.repo, ".qlty", "qlty.toml"), "invalid config", 0600)
				writeFixtureFile(t, filepath.Join(bin, "qlty"), "#!/bin/sh\nprintf 'invalid Qlty configuration\n' >&2\nexit 1\n", 0700)
			} else {
				writeFixtureFile(t, filepath.Join(bin, "qlty"), "#!/bin/sh\nexit 0\n", 0700)
			}
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(bytes.ToLower(body), []byte("qlty")) {
				t.Fatalf("missing Qlty failure: %s", body)
			}
			records, _ := f.server.Store.List(f.ctx, "projects")
			if len(records) != 0 {
				t.Fatal("invalid setup registered")
			}
		})
	}
}

func TestStarterCommitFailureBlocksRegistrationAndAllowsRetry(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "staged work\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", onboardingUserFile); err != nil {
		t.Fatal(err)
	}
	before, _ := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	writeFixtureFile(t, filepath.Join(f.repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\necho 'fixture commit refused' >&2\nexit 1\n", 0700)
	body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
	if !bytes.Contains(body, []byte("commit")) {
		t.Fatalf("commit failure missing: %s", body)
	}
	records, _ := f.server.Store.List(f.ctx, "projects")
	if len(records) != 0 {
		t.Fatal("failed commit registered project")
	}
	if _, err := os.Stat(filepath.Join(f.repo, onboardingPolicyYML)); !os.IsNotExist(err) {
		t.Fatalf("failed generated policy not cleaned up: %v", err)
	}
	after, _ := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if before != after {
		t.Fatal("failed commit altered user staging")
	}
	if err := os.Remove(filepath.Join(f.repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 201)
	if _, err := github.Run(f.ctx, f.repo, "git", "show", "HEAD:"+onboardingPolicyYML); err != nil {
		t.Fatal("retry did not commit policy:", err)
	}
}

func TestOnboardingDoesNotReplaceMissingStagedPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), "# staged user policy\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", "--", onboardingPolicyYML); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.repo, onboardingPolicyYML)); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
	staged, err := github.Run(f.ctx, f.repo, "git", "show", ":"+onboardingPolicyYML)
	if err != nil || staged != "# staged user policy" {
		t.Fatalf("staged policy overwritten: %q %v", staged, err)
	}
	if _, err = os.Stat(filepath.Join(f.repo, onboardingPolicyYML)); !os.IsNotExist(err) {
		t.Fatal("missing staged policy replaced")
	}
}

func TestStarterGitPreflightBlocksBeforeToolWrites(t *testing.T) {
	for _, state := range []string{"identity", "merge"} {
		t.Run(state, func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			configureStarterGitFailure(t, f, state)
			f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			for _, name := range []string{onboardingPolicyYML, ".qlty", ".codegraph"} {
				if _, err := os.Stat(filepath.Join(f.repo, name)); !os.IsNotExist(err) {
					t.Fatalf("Git preflight failure wrote %s", name)
				}
			}
		})
	}
}

func TestPolicySetupRepairsOnlyGeneratedLegacyStarter(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint("custom=", custom), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			legacy := prepareLegacyOnboardingPolicy(t, f, custom)
			status := 200
			if custom {
				status = 422
			}
			request := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/policy", strings.NewReader("{}")).WithContext(f.ctx)
			request.Header.Set("Idempotency-Key", store.ID())
			request.Header.Set("If-Match", "1")
			response := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(response, request)
			if response.Code != status {
				t.Fatalf("policy setup: %d %s", response.Code, response.Body)
			}
			body := response.Body.Bytes()
			if custom {
				if content, _ := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML)); string(content) != legacy {
					t.Fatal("custom policy overwritten")
				}
				return
			}
			assertUpgradedOnboardingPolicy(t, f, body)
		})
	}
}
