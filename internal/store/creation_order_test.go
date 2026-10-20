package store

import (
	"context"
	"encoding/json"
	"testing"
	"whirlwind/internal/model"
)

func creationOrder(t *testing.T, record model.Record) float64 {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	order, ok := value["created_order"].(float64)
	if !ok || order <= 0 {
		t.Fatalf("missing creation order: %s", data)
	}
	return order
}

func TestCreationOrderSurvivesUpdatesAcrossResourceKinds(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "directions", "old", model.Direction{ID: "old"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "inbox", "new", model.Notification{ID: "new"}, 0); err != nil {
		t.Fatal(err)
	}
	old, err := s.Get(ctx, "directions", "old")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Get(ctx, "inbox", "new")
	if err != nil {
		t.Fatal(err)
	}
	before := creationOrder(t, old)
	if before >= creationOrder(t, newer) {
		t.Fatal("creation order not shared across resource kinds")
	}
	if err := s.Put(ctx, "directions", "old", model.Direction{ID: "old", Selected: true}, old.Version); err != nil {
		t.Fatal(err)
	}
	old, err = s.Get(ctx, "directions", "old")
	if err != nil {
		t.Fatal(err)
	}
	if creationOrder(t, old) != before {
		t.Fatal("update changed creation order")
	}
	records, err := s.List(ctx, "directions")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || creationOrder(t, records[0]) != before {
		t.Fatal("list lost creation order")
	}
}
