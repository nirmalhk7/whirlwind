package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	oldExpandID       = "old-expand"
	oldGroupID        = "old-group"
	newGroupID        = "new-group"
	directionTestTime = "2026-11-10T09:00:00Z"
	directionNextTime = "2026-11-10T10:00:00Z"
	partialCategories = "2/4 categories"
	emptyCategories   = "0/4 categories"
	directionTaskID   = "task-1"
)

func generationRecord(order int64, updatedAt string, direction model.Direction) model.Record {
	data, err := json.Marshal(direction)
	if err != nil {
		panic(err)
	}
	return model.Record{CreatedOrder: order, ID: direction.ID, Version: 1, Data: data, UpdatedAt: updatedAt}
}

func generationDirection(id, group, category, title string) model.Direction {
	return model.Direction{
		ID: id, ProjectID: "p", GroupID: group, Day: "2026-11-10", Category: category,
		Status: "proposed", Title: title,
	}
}

func inboxLabels(d *Dashboard) []string {
	labels := make([]string, 0, d.inbox.GetItemCount())
	for index := 0; index < d.inbox.GetItemCount(); index++ {
		label, _ := d.inbox.GetItemText(index)
		labels = append(labels, label)
	}
	return labels
}

func TestInboxShowsOnlyNewestGenerationAndCountsMissingCategories(t *testing.T) {
	older := []model.Direction{
		generationDirection(oldExpandID, oldGroupID, "expand", "Old expansion"),
		generationDirection("old-optimize", oldGroupID, "optimize", "Old optimization"),
		generationDirection("old-harden", oldGroupID, "harden", "Old hardening"),
		generationDirection("old-simplify", oldGroupID, "simplify", "Old simplification"),
	}
	latest := []model.Direction{
		generationDirection("new-expand", newGroupID, "expand", "Current expansion"),
		generationDirection("new-optimize", newGroupID, "optimize", "Current optimization"),
	}
	records := make([]model.Record, 0, len(older)+len(latest))
	for index, direction := range older {
		records = append(records, generationRecord(int64(index+1), directionTestTime, direction))
	}
	for index, direction := range latest {
		records = append(records, generationRecord(int64(index+5), directionNextTime, direction))
	}

	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": records,
	})

	if len(d.directionRows) != 2 {
		t.Fatalf("Inbox has %d direction rows, want only the 2 newest proposals", len(d.directionRows))
	}
	for _, direction := range d.directionRows {
		if direction.GroupID != newGroupID {
			t.Errorf("Inbox mixed older generation %q into current generation", direction.GroupID)
		}
	}
	labels := strings.Join(inboxLabels(d), "\n")
	if !strings.Contains(labels, partialCategories) || !strings.Contains(labels, "Harden") || !strings.Contains(labels, "Simplify") {
		t.Fatalf("incomplete generation marker should report 2/4 and missing categories: %s", labels)
	}
}

func TestCompleteNewestGenerationShowsExactlyFourCategories(t *testing.T) {
	categories := []string{"expand", "optimize", "harden", "simplify"}
	records := make([]model.Record, 0, 8)
	for index, category := range categories {
		records = append(records, generationRecord(int64(index+1), directionTestTime, generationDirection("old-"+category, oldGroupID, category, "Old "+category)))
	}
	for index, category := range categories {
		records = append(records, generationRecord(int64(index+5), directionNextTime, generationDirection("new-"+category, newGroupID, category, "Current "+category)))
	}
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": records,
	})
	if len(d.directionRows) != 4 {
		t.Fatalf("Inbox has %d direction rows, want exactly 4 from the newest complete generation", len(d.directionRows))
	}
	want := strings.Join(categories, ",")
	got := make([]string, 0, len(d.directionRows))
	for _, direction := range d.directionRows {
		if direction.GroupID != newGroupID {
			t.Fatalf("complete Inbox includes older generation %q", direction.GroupID)
		}
		got = append(got, direction.Category)
	}
	if strings.Join(got, ",") != want {
		t.Fatalf("visible category order=%v, want %v", got, categories)
	}
	if len(inboxLabels(d)) != 4 {
		t.Fatalf("complete generation Inbox has %d rows, want exactly four category rows", len(inboxLabels(d)))
	}
}

func TestNoCandidateNoticeMarksNewestGenerationAsZeroOfFour(t *testing.T) {
	old := generationDirection(oldExpandID, oldGroupID, "expand", "Previous proposal")
	paused := model.Notification{
		ID: "paused", Kind: "ideation_paused",
		Message: "Repo: ideation incomplete; no worthwhile new direction for categories: expand, optimize, harden, simplify",
		LastAt:  directionNextTime,
	}
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {generationRecord(1, directionTestTime, old)},
		"inbox":      {record("paused", paused)},
	})
	if len(d.directionRows) != 0 {
		t.Fatalf("Inbox showed %d old proposals after a newer zero-candidate generation", len(d.directionRows))
	}
	labels := strings.Join(inboxLabels(d), "\n")
	if !strings.Contains(labels, emptyCategories) {
		t.Fatalf("latest empty generation lacks explicit 0/4 marker: %s", labels)
	}
}

func TestGenericPausePreservesSavedGenerationInsteadOfInventingZeroCategories(t *testing.T) {
	cases := []struct {
		name       string
		categories []string
	}{
		{name: "partial", categories: []string{"expand", "harden"}},
		{name: "complete", categories: []string{"expand", "optimize", "harden", "simplify"}},
	}
	for _, example := range cases {
		t.Run(example.name, func(t *testing.T) {
			records := make([]model.Record, 0, len(example.categories))
			for index, category := range example.categories {
				direction := generationDirection("current-"+category, "current-group", category, "Current "+category)
				records = append(records, generationRecord(int64(index+1), directionTestTime, direction))
			}
			paused := model.Notification{
				ID: "paused", Kind: "ideation_paused",
				Message: "Repo: ideation paused before a complete result; retry or update the Ideation ACP queue: provider unavailable",
				LastAt:  directionNextTime,
			}
			d := New()
			d.Present(control.Snapshot{
				"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
				"directions": records,
				"inbox":      {record("paused", paused)},
			})
			if len(d.directionRows) != len(example.categories) {
				t.Fatalf("generic pause hid %d/%d saved proposals", len(d.directionRows), len(example.categories))
			}
			labels := strings.Join(inboxLabels(d), "\n")
			if strings.Contains(labels, emptyCategories) {
				t.Fatalf("generic pause invented an empty generation: %s", labels)
			}
			if example.name == "partial" && !strings.Contains(labels, partialCategories) {
				t.Fatalf("partial saved generation lost its truthful 2/4 count: %s", labels)
			}
		})
	}
}

func TestPartialRunNoticeDoesNotTurnItsSavedProposalsIntoZeroOfFour(t *testing.T) {
	partial := model.Notification{
		ID: "partial", Kind: "ideation_paused",
		Message: "Repo: partial ideation saved; no worthwhile new direction for categories: harden, simplify",
		LastAt:  directionNextTime,
	}
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {
			generationRecord(1, directionTestTime, generationDirection("partial-expand", "partial-group", "expand", "Current expansion")),
			generationRecord(2, directionTestTime, generationDirection("partial-optimize", "partial-group", "optimize", "Current optimization")),
		},
		"inbox": {record("partial", partial)},
	})
	if len(d.directionRows) != 2 {
		t.Fatalf("partial run notice hid %d saved proposals, want 2", len(d.directionRows))
	}
	labels := strings.Join(inboxLabels(d), "\n")
	if !strings.Contains(labels, partialCategories) || strings.Contains(labels, emptyCategories) {
		t.Fatalf("partial run notice produced an untruthful category count: %s", labels)
	}
}

func TestNewestGenerationFilteringPreservesActiveTaskInboxEntries(t *testing.T) {
	older := generationDirection(oldExpandID, oldGroupID, "expand", "Old proposal")
	current := generationDirection("new-expand", newGroupID, "expand", "Current proposal")
	notice := model.Notification{ID: "n", Role: "code", TaskID: directionTaskID, Kind: "coding", Message: "Agent needs attention"}
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {
			generationRecord(1, directionTestTime, older),
			generationRecord(2, directionNextTime, current),
		},
		"execution_plans": {record(directionTaskID, model.ExecutionPlan{ID: directionTaskID, ProjectID: "p", Stage: "paused"})},
		"inbox":           {record("n", notice)},
	})
	labels := strings.Join(inboxLabels(d), "\n")
	if !strings.Contains(labels, "Current proposal") || !strings.Contains(labels, "Coding - Agent needs attention") || strings.Contains(labels, "Old proposal") {
		t.Fatalf("generation filtering changed current directions or active task entries: %s", labels)
	}
}

func TestLegacyProposalsRemainCurrentAndAreMarkedUnknown(t *testing.T) {
	legacy := generationDirection("legacy-proposed", "", "expand", "Legacy proposal")
	legacy.Day = ""
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {generationRecord(1, directionTestTime, legacy)},
	})
	if len(d.directionRows) != 1 || d.directionRows[0].ID != legacy.ID {
		t.Fatalf("legacy proposal disappeared from current Inbox: %#v", d.directionRows)
	}
	labels := strings.Join(inboxLabels(d), "\n")
	if !strings.Contains(labels, "Legacy") || !strings.Contains(labels, "generation unknown") {
		t.Fatalf("legacy current proposal lacks unknown-generation label: %s", labels)
	}
}
