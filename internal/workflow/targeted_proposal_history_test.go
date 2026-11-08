package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestNoveltyReviewRetrievesCandidateSpecificImplementedHistory(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	old := model.Direction{ID: "implemented-credential-refresh", ProjectID: prepared.project.ID, Day: "2026-10-01", Category: "harden", Title: "Expired credential refresh", Problem: "Expired credential refresh prompts users to sign in again", Mechanism: "Refresh scoped credentials before protected requests", Components: []string{"internal/auth/retired"}, RepositoryRevision: "rev-old"}
	if err := prepared.engine.Store.SaveDirections(ctx, old.ProjectID, old.Day, []model.Direction{old}); err != nil {
		t.Fatal(err)
	}
	if err := prepared.engine.Store.UpdateProposalOutcome(ctx, old.ProjectID, old.ID, "implemented", "", nil); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		item := model.Direction{ID: fmt.Sprintf("unrelated-%02d", i), ProjectID: prepared.project.ID, Day: "2026-10-02", Category: "optimize", Title: "Unrelated area", Problem: fmt.Sprintf("Unrelated cache issue %d", i), Mechanism: fmt.Sprintf("Tune the unrelated cache %d", i), Components: []string{"internal/cache"}}
		if err := prepared.engine.Store.SaveDirections(ctx, item.ProjectID, item.Day, []model.Direction{item}); err != nil {
			t.Fatal(err)
		}
	}
	prepared.fixture.batch = ideaBatch(t)
	prepared.fixture.batch[2].Problem = "Expired credential refresh can prompt users to sign in again"
	prepared.fixture.batch[2].Mechanism = "Renew scoped credentials before protected requests"
	prepared.fixture.batch[2].Components = []string{"internal/auth/retired"}
	if err := prepared.engine.Ideate(ctx, prepared.project, "2026-10-03"); err != nil {
		t.Fatal(err)
	}
	if len(prepared.fixture.reviewPrompts) != 1 || !strings.Contains(prepared.fixture.reviewPrompts[0], old.ID) || !strings.Contains(prepared.fixture.reviewPrompts[0], "implemented") {
		t.Fatalf("novelty review omitted the candidate's old implemented match: %#v", prepared.fixture.reviewPrompts)
	}
}

func TestNoveltyReviewRetainsLongCandidateMatchedRejectedRecord(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	problem := "Authentication refresh failures strand users during protected requests when a previously valid session expires and recovery is attempted only after the request has already failed."
	mechanism := "Refresh the scoped session before protected requests, preserve the original request, and retry only after the refreshed credential is validated against the current account state."
	components := []string{"internal/auth/session", "internal/http/client"}
	old := model.Direction{ID: "rejected-long-session-refresh", ProjectID: prepared.project.ID, Day: "2026-10-01", Category: "harden", Title: "Session recovery", Problem: problem, Mechanism: mechanism, Components: components, Evidence: []model.EvidenceRef{{Kind: "code", Ref: "internal/auth/session", Summary: strings.Repeat("Observed expired-session recovery gap ", 3)}}, RepositoryRevision: "old-revision", Status: "rejected", RejectionReason: "The credential refresh was not authorized for this connection."}
	if err := prepared.engine.Store.SaveDirections(ctx, old.ProjectID, old.Day, []model.Direction{old}); err != nil {
		t.Fatal(err)
	}
	if err := prepared.engine.Store.UpdateProposalOutcome(ctx, old.ProjectID, old.ID, "rejected", old.RejectionReason, nil); err != nil {
		t.Fatal(err)
	}
	candidate := &prepared.fixture.batch[0]
	candidate.Problem = problem
	candidate.Mechanism = "Recover an expired session before replaying a protected request after validating the refreshed account credential."
	candidate.Components = components
	candidate.RepositoryRevision = "new-revision"
	if err := prepared.engine.Ideate(ctx, prepared.project, "2026-10-29"); err != nil {
		t.Fatal(err)
	}
	if len(prepared.fixture.reviewPrompts) == 0 || !strings.Contains(prepared.fixture.reviewPrompts[0], old.ID) || !strings.Contains(prepared.fixture.reviewPrompts[0], old.RejectionReason) {
		t.Fatalf("candidate-matched long rejected record was omitted from semantic review: %s", strings.Join(prepared.fixture.reviewPrompts, "\n"))
	}
}

func TestIdeationPausesWhenTotalHistoryBudgetCannotFitComparison(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	settingsRecord, err := prepared.engine.Store.Get(ctx, "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Decode[Settings](settingsRecord)
	if err != nil {
		t.Fatal(err)
	}
	settings.Ideation.HistoryRunTokenBudget = 1
	if err := prepared.engine.Store.Put(ctx, "settings", "global", settings, settingsRecord.Version); err != nil {
		t.Fatal(err)
	}
	if err := prepared.engine.Ideate(ctx, prepared.project, "2026-10-30"); err == nil || !strings.Contains(err.Error(), "history budget exhausted") {
		t.Fatalf("history exhaustion was not reported as incomplete: %v", err)
	}
	records, err := prepared.engine.Store.List(ctx, "directions")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		var direction model.Direction
		if json.Unmarshal(record.Data, &direction) == nil && direction.ProjectID == prepared.project.ID && direction.Day == "2026-10-30" {
			t.Fatalf("budget-exhausted ideation published a proposal: %+v", direction)
		}
	}
	notifications, err := prepared.engine.Store.List(ctx, "inbox")
	if err != nil {
		t.Fatal(err)
	}
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
