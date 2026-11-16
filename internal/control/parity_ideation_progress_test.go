package control

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityIdeationInspectionRetainsDirectionCountersOnlineAndOffline(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if err := s.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "queued"}, 0); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.ReserveIdeaCandidate(ctx, "source", "expand")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIdeaCandidate(ctx, id, nil, "similar historical idea"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveIdeaCandidate(ctx, "source", "expand"); err != nil {
		t.Fatal(err)
	}

	offline, err := Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	assertIdeationDirectionProgress(t, offline["operations"])
	response, err := (SupervisorDispatcher{DB: s.DB}).Handle(ctx, Request{RequestID: "progress", Operation: "inspect.records", ResourceID: "operations", ProjectID: "p", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertIdeationDirectionProgress(t, response.Result.(RecordPage).Records)
}

func TestParityIdeationProgressPreservesBoundedInspectionPages(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	for n := 0; n < 20; n++ {
		if err := s.Put(ctx, "operations", fmt.Sprintf("source-%02d", n), model.Operation{Kind: "ideation", Subject: "p", Stage: "queued"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec("UPDATE ideation_directions SET error_reason=?", strings.Repeat("retained failure evidence ", 1000)); err != nil {
		t.Fatal(err)
	}
	response, err := (SupervisorDispatcher{DB: s.DB}).Handle(ctx, Request{RequestID: "progress", Operation: "inspect.records", ResourceID: "operations", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	page := response.Result.(RecordPage)
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 512<<10 || !page.HasMore || page.NextCursor == 0 {
		t.Fatalf("enriched progress escaped bounded pagination: bytes=%d more=%t cursor=%d", len(raw), page.HasMore, page.NextCursor)
	}
}

func assertIdeationDirectionProgress(t *testing.T, records []model.Record) {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("source run unavailable: %+v", records)
	}
	var progress struct {
		Directions []struct {
			Direction      string
			CandidateCount int `json:"candidate_count"`
			Status         string
		} `json:"ideation_directions"`
	}
	if err := json.Unmarshal(records[0].Data, &progress); err != nil {
		t.Fatal(err)
	}
	if len(progress.Directions) != 4 {
		t.Fatalf("direction counters unavailable: %s", records[0].Data)
	}
	for _, direction := range progress.Directions {
		if direction.Direction == "expand" && (direction.CandidateCount != 2 || direction.Status != "running") {
			t.Fatalf("direction state lost: %+v", direction)
		}
	}
}
