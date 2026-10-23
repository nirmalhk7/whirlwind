package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func TestProviderStatusWhileStoppedNeverSpawnsProcesses(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "spawned")
	settings := workflow.DefaultSettings()
	settings.Stopped = true
	settings.ProviderStatusCheckSeconds = 60
	if err := s.Store.Put(ctx, "settings", "global", settings, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "providers", "probe", model.Provider{ID: "probe", Name: "Probe", Kind: "acp", Command: []string{"sh", "-c", "touch \"$1\"", "sh", marker}}, 0); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/provider-status", nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got struct {
		Interval  int  `json:"interval_seconds"`
		Stopped   bool `json:"stopped"`
		Providers []struct {
			Status string `json:"status"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Interval != 60 || !got.Stopped || len(got.Providers) != 1 || got.Providers[0].Status != "paused" {
		t.Fatalf("wrong status: %s", w.Body)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("provider process launched while stopped: %v", err)
	}
}

func TestProviderStatusCachesChecksAndInvalidatesOnConfigurationAndStop(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	p := model.Provider{ID: "probe", Name: "Probe", Kind: "claude", Command: []string{"sh", "-c", `read -r request; printf '%s\n' '{"id":1,"result":{"models":[{"id":"fixture"}]}}'; cat >/dev/null`}}
	if err := s.Store.Put(ctx, "providers", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	get := func(want string) string {
		t.Helper()
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/provider-status", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"`+want+`"`) {
			t.Fatalf("status response: %d %s", w.Code, w.Body)
		}
		var response workflow.ProviderStatuses
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Providers) != 1 {
			t.Fatalf("provider evidence: %s %v", w.Body, err)
		}
		return response.Providers[0].CheckedAt
	}
	checkedAt := get("working")
	if cached := get("working"); cached != checkedAt {
		t.Fatalf("cached check replaced its observation time: %s %s", checkedAt, cached)
	}
	p.Command = []string{"sh", "-c", `read -r request; printf '%s\n' '{"id":1,"error":{"code":-32000,"message":"Authentication required"}}'; cat >/dev/null`}
	if err := s.Store.Put(ctx, "providers", p.ID, p, 1); err != nil {
		t.Fatal(err)
	}
	if refreshed := get("failed"); refreshed == checkedAt {
		t.Fatal("changed configuration retained stale observation")
	}
	settings := workflow.DefaultSettings()
	settings.Stopped = true
	if err := s.Store.Put(ctx, "settings", "global", settings, 0); err != nil {
		t.Fatal(err)
	}
	get("paused")
}
