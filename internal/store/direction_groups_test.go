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
	directionTestDay      = "2026-10-19"
	legacyTaskID          = "direction-idea1"
	directionTestWorktree = "/saved-work"
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

func TestUpgradePreservesOriginalGroupsAfterSelectionChangesTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writeLegacyDirections(t, s, ctx)
	// Selection changes must not pull old ideas into a newer generation.
	record, _ := s.Get(ctx, "directions", "idea0")
	d, _ := Decode[model.Direction](record)
	if err := s.Put(ctx, "directions", record.ID, d, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "tasks", legacyTaskID, model.Task{ID: legacyTaskID, Stage: "paused", ResumeStage: "coding", Error: "Paused by global runner stop", Worktree: directionTestWorktree}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DELETE FROM migrations WHERE version>5"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	a, _ := s.Get(ctx, "directions", "idea0")
	b, _ := s.Get(ctx, "directions", "idea4")
	assertLegacyDirectionGroups(t, s, ctx, a, b)
	record, _ = s.Get(ctx, "tasks", legacyTaskID)
	task, _ := Decode[model.Task](record)
	if task.Stage != "paused" || task.ResumeStage != "coding" || task.Worktree != directionTestWorktree || task.Error != "Direction deselected" {
		t.Fatalf("old choice not paused safely: %+v", task)
	}
}

func writeLegacyDirections(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("idea%d", i)
		direction := model.Direction{ID: id, ProjectID: "p", Day: directionTestDay, Selected: i < 2}
		if err := s.Put(ctx, "directions", id, direction, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func assertLegacyDirectionGroups(t *testing.T, s *Store, ctx context.Context, first, second model.Record) {
	t.Helper()
	if groupID(t, first) == "" || groupID(t, first) == groupID(t, second) {
		t.Fatal("legacy generations merged")
	}
	for i := 0; i < 4; i++ {
		record, _ := s.Get(ctx, "directions", fmt.Sprintf("idea%d", i))
		if groupID(t, record) != groupID(t, first) {
			t.Fatal("legacy generation split")
		}
		direction, _ := Decode[model.Direction](record)
		if direction.Selected != (i == 0) {
			t.Fatal("multiple legacy selections retained")
		}
	}
}
