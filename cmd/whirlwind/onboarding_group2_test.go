package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (f onboardingFixture) verifyIdeation(t *testing.T, project model.Project, chosen string) {
	t.Helper()
	var queued struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(f.request(t, "POST", "/projects/"+project.ID+"/ideation", map[string]any{}, 202), &queued); err != nil || queued.ID == "" {
		t.Fatalf("ideation was not queued: %+v, %v", queued, err)
	}
	waitOnboardingOperation(t, f, queued.ID)
	var directions []model.Record
	if err := json.Unmarshal(f.request(t, "GET", "/directions", nil, 200), &directions); err != nil || len(directions) != 4 {
		t.Fatalf("agent reply not applied: %d directions, %v", len(directions), err)
	}
	var sessions []model.Record
	if err := json.Unmarshal(f.request(t, "GET", "/sessions", nil, 200), &sessions); err != nil || len(sessions) != 1 {
		t.Fatalf("agent session not persisted: %d sessions, %v", len(sessions), err)
	}
	var result struct {
		Provider  string `json:"provider"`
		SessionID string `json:"session_id"`
		Summary   string `json:"summary"`
		Finished  bool   `json:"finished"`
	}
	summaries := map[string]bool{}
	for _, session := range sessions {
		if err := json.Unmarshal(session.Data, &result); err != nil || result.Provider != chosen || result.SessionID != onboardingSessionID || !result.Finished {
			t.Fatalf("final reply/session lost: %+v, %v", result, err)
		}
		summaries[result.Summary] = true
	}
	if !summaries[onboardingReply] {
		t.Fatalf("generation missing: %v", summaries)
	}
}

func waitOnboardingOperation(t *testing.T, f onboardingFixture, id string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := f.server.Store.Get(f.ctx, "operations", id)
		if err != nil {
			t.Fatal(err)
		}
		var operation struct{ Stage, Error string }
		if err := json.Unmarshal(record.Data, &operation); err != nil {
			t.Fatal(err)
		}
		if operation.Stage == "complete" {
			return
		}
		if operation.Stage == "failed" {
			t.Fatal("onboarded agent failed:", operation.Error)
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("onboarding timed out:", f.ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestGoOnboardingRejectsInvalidSetup(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "invalid", Name: "Invalid", Kind: "codex", Command: []string{"codex"}}, 422)
	f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "empty", Name: "Empty", Kind: "acp"}, 422)
	var missing model.Record
	if err := json.Unmarshal(f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "missing", Name: "Missing", Kind: "acp", Command: []string{filepath.Join(t.TempDir(), "missing-agent")}}, 201), &missing); err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](missing)
	if err != nil || p.Available {
		t.Fatalf("missing executable marked available: %+v, %v", p, err)
	}
	policyPath := filepath.Join(f.repo, onboardingPolicyYAML)
	committed, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, policyPath, string(committed)+"# uncommitted\n", 0600)
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Uncommitted", "path": f.repo}, 422)
	if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Missing policy", "path": f.repo}, 422)
}
