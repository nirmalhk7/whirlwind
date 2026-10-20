package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestManualIdeationWaitsForActiveGeneration(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	var record model.Record
	if err := json.Unmarshal(f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Fixture", "path": f.repo}, 201), &record); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		t.Fatal(err)
	}
	key := "generation:" + project.ID
	lease, err := f.server.Store.Claim(f.ctx, key, "scheduled", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var queued struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(f.request(t, "POST", "/projects/"+project.ID+"/ideation", map[string]any{}, 202), &queued); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	operation, err := f.server.Store.Get(f.ctx, "operations", queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(operation.Data, &status); err != nil || status.Stage != "running" {
		t.Fatalf("manual generation did not wait: %s %v", operation.Data, err)
	}
	if err := f.server.Store.Release(f.ctx, key, "scheduled", lease.Fence); err != nil {
		t.Fatal(err)
	}
	waitOnboardingOperation(t, f, queued.ID)
}

func removeOnboardingPolicy(t *testing.T, f onboardingFixture) {
	t.Helper()
	for _, args := range [][]string{{"rm", onboardingPolicyYAML}, {"-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Remove policy"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOnboardingBlocksGHBeforeRepositoryWrites(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			if missing {
				if err := os.Remove(filepath.Join(bin, "gh")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
			} else {
				writeFixtureFile(t, filepath.Join(bin, "gh"), "#!/bin/sh\nprintf 'gh authentication required\n' >&2\nexit 1\n", 0700)
			}
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(body, []byte("gh")) {
				t.Fatalf("wrong preflight failure: %s", body)
			}
			assertBlockedOnboardingFiles(t, f)
		})
	}
}

func TestOnboardingCommitsOnlyNewStarterPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	for _, args := range [][]string{{"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@localhost"}, {"config", "commit.gpgsign", "false"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "staged user work\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", onboardingUserFile); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "unstaged user work\n", 0600)
	writeFixtureFile(t, filepath.Join(f.repo, "main.go"), "package main // user edit\n", 0600)
	before, err := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 201)
	committed, err := github.Run(f.ctx, f.repo, "git", "show", "HEAD:"+onboardingPolicyYML)
	if err != nil || !strings.Contains(committed, "qlty") {
		t.Fatalf("starter not committed: %s %v", committed, err)
	}
	files, err := github.Run(f.ctx, f.repo, "git", "show", "--format=", "--name-only", "HEAD")
	if err != nil || files != ".qlty/qlty.toml\n"+onboardingPolicyYML {
		t.Fatalf("commit included other work: %q %v", files, err)
	}
	after, err := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if err != nil || before != after {
		t.Fatal("user staging changed")
	}
	if content, err := os.ReadFile(filepath.Join(f.repo, onboardingUserFile)); err != nil || string(content) != "unstaged user work\n" {
		t.Fatal("user work changed")
	}
	head, _ := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 409)
	next, _ := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
	if next != head {
		t.Fatal("retry created another commit")
	}
}
