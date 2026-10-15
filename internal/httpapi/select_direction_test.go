package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestSelectIdeaWithEmptyPolicyWaitsForPolicySetup(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Harden recovery"}, 0); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/directions/idea/select", bytes.NewBufferString("{}"))
	r.Header.Set("Idempotency-Key", "select-one")
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var record model.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil || task.Stage != "paused" || task.ResumeStage != "planning" || task.Error == "" {
		t.Fatalf("idea execution not held for policy: %+v %v", task, err)
	}
	saved, err := s.Store.Get(ctx, "directions", "idea")
	if err != nil {
		t.Fatal(err)
	}
	direction, err := store.Decode[model.Direction](saved)
	if err != nil || !direction.Selected {
		t.Fatalf("selection lost: %+v %v", direction, err)
	}
}
