package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
	"whirlwind/internal/workflow"
)

type connectivityAdapter struct {
	release <-chan struct{}
	fail    bool
}

func (a connectivityAdapter) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	select {
	case <-ctx.Done():
		return provider.Output{}, ctx.Err()
	case <-a.release:
	}
	if a.fail {
		return provider.Output{}, errors.New("authentication required")
	}
	return provider.Output{Text: "Hello World", SessionID: "connection-one"}, nil
}

func TestProviderConnectionQueuesAndPersistsResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "reply", true: "failure"}[fail], func(t *testing.T) {
			runProviderConnectionScenario(t, fail)
		})
	}
}

func TestProviderConnectionRequiresCurrentRunnableProvider(t *testing.T) {
	s := setup(t)
	p := model.Provider{ID: "disabled", Kind: "unavailable"}
	if err := s.Store.Put(context.Background(), "providers", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version string
		status  int
	}{{"", 428}, {"99", 409}, {"1", 422}} {
		request := httptest.NewRequest("POST", "http://localhost/api/v1/providers/disabled/test", bytes.NewBufferString(`{}`))
		request.Header.Set("Idempotency-Key", store.ID())
		request.Header.Set("If-Match", tc.version)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("version %q: got %d %s, want %d", tc.version, response.Code, response.Body, tc.status)
		}
	}
}

type connectionTestResult struct {
	Stage, Provider, Text, Error string
	SessionID                    string `json:"session_id"`
}

func runProviderConnectionScenario(t *testing.T, fail bool) {
	t.Helper()
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	s := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	settings := workflow.DefaultSettings()
	settings.Admission = model.AdmissionSettings{DefaultBudget: &model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60}, Pools: map[string]model.QuotaReserve{"chosen": {MaxAgeSeconds: 60}}}
	if err := s.Store.Put(ctx, "settings", "global", settings, 0); err != nil {
		t.Fatal(err)
	}
	s.Context = ctx
	release := make(chan struct{})
	s.Engine.Adapter = func(model.Provider) provider.Adapter { return connectivityAdapter{release, fail} }
	p := model.Provider{ID: "chosen", Name: "Chosen", Kind: "acp", Command: []string{"fixture"}}
	if err := s.Store.Put(ctx, "providers", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	operationID := queueConnectionTest(t, s)
	_, result := pendingConnectionTest(t, s, ctx, operationID, p.ID)
	close(release)
	result = awaitConnectionTest(t, s, ctx, operationID, result)
	assertConnectionTestOutcome(t, result, fail)
}

func queueConnectionTest(t *testing.T, s *Server) string {
	t.Helper()
	send := func(version string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://localhost/api/v1/providers/chosen/test", bytes.NewBufferString(`{}`))
		request.Header.Set("Idempotency-Key", "connection-test")
		request.Header.Set("If-Match", version)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		return response
	}
	response := send("1")
	if response.Code != 202 {
		t.Fatalf("test not queued: %d %s", response.Code, response.Body)
	}
	if replay := send("1"); replay.Code != 202 || replay.Body.String() != response.Body.String() {
		t.Fatalf("retry started another test: %d %s", replay.Code, replay.Body)
	}
	var queued struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil || queued.ID == "" {
		t.Fatalf("operation ID missing: %+v, %v", queued, err)
	}
	return queued.ID
}

func pendingConnectionTest(t *testing.T, s *Server, ctx context.Context, operationID, providerID string) (model.Record, connectionTestResult) {
	t.Helper()
	record, err := s.Store.Get(ctx, "operations", operationID)
	if err != nil {
		t.Fatal(err)
	}
	var result connectionTestResult
	if err := json.Unmarshal(record.Data, &result); err != nil || result.Stage != "running" || result.Provider != providerID {
		t.Fatalf("pending state missing: %+v, %v", result, err)
	}

	var metadata map[string]any
	_ = json.Unmarshal(record.Data, &metadata)
	if metadata["owner"] == nil || metadata["input"] == nil || metadata["started_at"] == nil {
		t.Fatalf("operation recovery metadata missing: %s", record.Data)
	}
	return record, result
}

func awaitConnectionTest(t *testing.T, s *Server, ctx context.Context, operationID string, result connectionTestResult) connectionTestResult {
	t.Helper()
	for result.Stage == "running" {
		select {
		case <-ctx.Done():
			t.Fatal("test result never saved")
		case <-time.After(10 * time.Millisecond):
		}
		var record model.Record
		request := httptest.NewRequest("GET", "http://localhost/api/v1/operations/"+operationID, nil)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &record) != nil || json.Unmarshal(record.Data, &result) != nil {
			t.Fatalf("result unreadable: %d %s", response.Code, response.Body)
		}
	}
	return result
}

func assertConnectionTestOutcome(t *testing.T, result connectionTestResult, fail bool) {
	t.Helper()
	if fail {
		if result.Stage != "failed" || result.Error != "authentication required" {
			t.Fatalf("failure missing: %+v", result)
		}
	} else if result.Stage != "complete" || result.Text != "Hello World" || result.SessionID != "connection-one" {
		t.Fatalf("reply missing: %+v", result)
	}
}
