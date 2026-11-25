package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestIdeationPausesWhenTotalHistoryBudgetCannotFitContext(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	settingsRecord, err := prepared.engine.Store.Get(ctx, "settings", "global")
	requireWorkflowSuccess(t, err)
	settings, err := store.Decode[Settings](settingsRecord)
	requireWorkflowSuccess(t, err)
	settings.Ideation.HistoryRunTokenBudget = 1
	requireWorkflowSuccess(t, prepared.engine.Store.Put(ctx, "settings", "global", settings, settingsRecord.Version))
	if err := prepared.engine.Ideate(ctx, prepared.project, "2026-10-30"); err == nil || !strings.Contains(err.Error(), "history budget exhausted") {
		t.Fatalf("history exhaustion was not reported as incomplete: %v", err)
	}
	records, err := prepared.engine.Store.List(ctx, "directions")
	requireWorkflowSuccess(t, err)
	for _, record := range records {
		var direction model.Direction
		if json.Unmarshal(record.Data, &direction) == nil && direction.ProjectID == prepared.project.ID && direction.Day == "2026-10-30" {
			t.Fatalf("budget-exhausted ideation published a proposal: %+v", direction)
		}
	}
	notifications, err := prepared.engine.Store.List(ctx, "inbox")
	requireWorkflowSuccess(t, err)
	paused := false
	for _, record := range notifications {
		var notice model.Notification
		if json.Unmarshal(record.Data, &notice) == nil && notice.Kind == "ideation_paused" && strings.Contains(notice.Message, "history budget exhausted") {
			paused = true
		}
	}
	if !paused {
		t.Fatal("history exhaustion did not persist an actionable pause notification")
	}
}
