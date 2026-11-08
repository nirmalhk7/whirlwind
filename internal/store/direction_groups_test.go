package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

const (
	directionTestDay = "2026-10-19"
)

func groupID(t *testing.T, record model.Record) string {
	t.Helper()
	var data struct {
		GroupID string `json:"group_id"`
	}
	if err := json.Unmarshal(record.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.GroupID
}

func TestSaveDirectionsKeepsSameDayBatchesSeparate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	ctx := context.Background()
	for _, prefix := range []string{"a", "b"} {
		directions := []model.Direction{}
		for i, category := range []string{"expand", "optimize", "harden", "simplify"} {
			directions = append(directions, model.Direction{ID: fmt.Sprintf("%s%d", prefix, i), ProjectID: "p", Day: directionTestDay, Category: category})
		}
		if err := s.SaveDirections(ctx, "p", directionTestDay, directions); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := s.Get(ctx, "directions", "a0")
	b, _ := s.Get(ctx, "directions", "b0")
	if groupID(t, a) == "" || groupID(t, a) == groupID(t, b) {
		t.Fatal("generation batches lack distinct IDs")
	}
	for i := 1; i < 4; i++ {
		record, _ := s.Get(ctx, "directions", fmt.Sprintf("a%d", i))
		if groupID(t, record) != groupID(t, a) {
			t.Fatal("batch split")
		}
	}
}

func TestPartialDirectionRunIsPersistedAsIncomplete(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "partial.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	partial := []model.Direction{{ID: "expand-1", Category: "expand", Title: "Expand", Description: "Evidence-backed expansion"}}
	if err := s.SaveDirections(ctx, "p", directionTestDay, partial); err != nil {
		t.Fatal(err)
	}
	record, err := s.Get(ctx, "schedules", "p:"+directionTestDay)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Stage             string   `json:"stage"`
		MissingCategories []string `json:"missing_categories"`
	}
	if err := json.Unmarshal(record.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Stage != "incomplete" {
		t.Fatalf("partial run stage=%q, want incomplete", state.Stage)
	}
	want := []string{"optimize", "harden", "simplify"}
	if fmt.Sprint(state.MissingCategories) != fmt.Sprint(want) {
		t.Fatalf("missing categories=%v, want %v", state.MissingCategories, want)
	}
}
