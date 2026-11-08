package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/store"
)

func TestInspectionPagesAreBoundedProjectScopedAndCursorStable(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for i := range 5 {
		project := "other"
		if i < 4 {
			project = "target"
		}
		if err := db.Put(ctx, "tasks", string(rune('a'+i)), map[string]string{"project_id": project}, 0); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	first, err := dispatcher.records(ctx, Request{ResourceID: "tasks", ProjectID: "target", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 2 || !first.HasMore || first.NextCursor <= first.Cursor {
		t.Fatalf("first page is not bounded/scoped: %+v", first)
	}
	second, err := dispatcher.records(ctx, Request{ResourceID: "tasks", ProjectID: "target", EventCursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 2 || second.HasMore {
		t.Fatalf("second page did not advance: %+v", second)
	}
	for _, record := range append(first.Records, second.Records...) {
		var value map[string]string
		if err := json.Unmarshal(record.Data, &value); err != nil {
			t.Fatal(err)
		}
		if value["project_id"] != "target" {
			t.Fatalf("cross-project record leaked: %s", record.Data)
		}
	}
}

func TestLocalDispatcherRejectsRawResourceKindsAndBoundsEvents(t *testing.T) {
	var database *sql.DB
	dispatcher := SupervisorDispatcher{DB: database}
	if _, err := dispatcher.Handle(context.Background(), Request{Operation: "inspect.records", ResourceID: "idempotency"}); err == nil {
		t.Fatal("internal store kind became locally inspectable")
	}
	if _, err := dispatcher.Handle(context.Background(), Request{Operation: "inspect.events", Limit: 501}); err == nil {
		t.Fatal("unbounded event request was accepted")
	}
}

func TestOfflineSnapshotPaginatesAllRowsAndEvents(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for i := range 106 {
		if err := db.Put(ctx, "questions", fmt.Sprintf("question-%03d", i), map[string]any{"status": "resolved"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := (SupervisorDispatcher{DB: db.DB}).Snapshot(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot["questions"]) != 106 {
		t.Fatalf("offline snapshot retained %d questions, want all 106", len(snapshot["questions"]))
	}
	if len(snapshot["events"]) < 106 {
		t.Fatalf("offline snapshot retained %d events, want all event pages", len(snapshot["events"]))
	}
	if snapshot["questions"][105].ID != "question-105" {
		t.Fatalf("offline snapshot omitted newest row: %q", snapshot["questions"][105].ID)
	}
}

func TestInspectionPagesRespectByteBudgetAndRetainLargeRows(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	largeValue := strings.Repeat("v", 32<<10)
	for i := range 80 {
		if err := db.Put(ctx, "verification", fmt.Sprintf("verification-%03d", i), map[string]string{"output": largeValue}, 0); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	page, err := dispatcher.records(ctx, Request{ResourceID: "verification", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 512<<10 {
		t.Fatalf("inspection page is %d bytes, budget is 524288", len(encoded))
	}
	if !page.HasMore || len(page.Records) >= 80 || page.NextCursor <= 0 {
		t.Fatalf("large records did not produce a bounded continuing page: rows=%d has_more=%t cursor=%d", len(page.Records), page.HasMore, page.NextCursor)
	}
	all, err := dispatcher.allRecords(ctx, "verification", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 80 || all[len(all)-1].ID != "verification-079" {
		t.Fatalf("byte paging lost records: count=%d newest=%q", len(all), all[len(all)-1].ID)
	}
}

func TestEventPagesKeepArrayContractAndSplitLargeHistoryByBytes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	largeValue := strings.Repeat("event payload ", 2300)
	for i := range 80 {
		if err := db.Put(ctx, "verification", fmt.Sprintf("verification-%03d", i), map[string]string{"output": largeValue}, 0); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	first, err := dispatcher.events(ctx, Request{Operation: "inspect.events", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 512<<10 {
		t.Fatalf("event array page is %d bytes, want at most 524288", len(encoded))
	}
	if len(first) == 0 || len(first) >= 80 {
		t.Fatalf("large event history was not byte-split into a short continuing array page: %d events", len(first))
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("inspect.events no longer returns an array: %v", err)
	}
	all, err := dispatcher.allEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 80 {
		t.Fatalf("short byte pages lost event history: got %d, want 80", len(all))
	}
}
