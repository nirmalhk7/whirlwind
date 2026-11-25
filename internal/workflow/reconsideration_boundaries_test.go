package workflow

import (
	"context"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	firstReconsiderationDay   = "2026-11-06"
	nextReconsiderationDay    = "2026-11-07"
	proposalSuppressionReason = "Human declined this proposal; no reconsideration was requested."
)

func TestIdeationCannotAuthorizeUnchangedSuppressedProposalFromModelClaims(t *testing.T) {
	for _, outcome := range []string{"rejected", "dismissed"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			prepared := prepareNoveltyTest(t, []string{`{"unique":true}`, `{"unique":true}`})
			previous := persistSuppressedProposal(t, ctx, prepared, outcome)
			assertCurrentReconsiderationEvidence(t, ctx, prepared, previous)
			assertNoReconsiderationAuthorization(t, ctx, prepared, "fixture unexpectedly contains a host reconsideration authorization")

			batch, distinctCategories := modelClaimingReconsideration(t, previous)
			prepared.fixture.batch = batch
			generationsBefore := prepared.fixture.generations
			ideationErr := prepared.engine.Ideate(WithFreshDiscovery(ctx, true), prepared.project, nextReconsiderationDay)
			if prepared.fixture.generations <= generationsBefore {
				t.Fatalf("second ideation short-circuited instead of exercising model output: generations=%d", prepared.fixture.generations)
			}

			seen := distinctUnselectedDirections(t, ctx, prepared, previous)
			if ideationErr != nil {
				t.Fatalf("historical similarity suppressed a valid candidate: %v", ideationErr)
			}
			for _, category := range distinctCategories {
				if !seen[category] {
					t.Errorf("valid category %q was dropped", category)
				}
			}
			assertSuppressedProposal(t, ctx, prepared, previous, outcome)
			assertNoReconsiderationAuthorization(t, ctx, prepared, "model output created supervisor authorization")
		})
	}
}

func persistSuppressedProposal(t *testing.T, ctx context.Context, prepared noveltyTestFixture, outcome string) model.Direction {
	t.Helper()
	_, _, primed, unchanged, err := prepared.engine.prepareDiscovery(WithFreshDiscovery(ctx, true), prepared.project)
	if err != nil || unchanged {
		t.Fatalf("fixture did not produce discovery state to prime: unchanged=%v err=%v", unchanged, err)
	}
	if err := prepared.engine.saveDiscoverySnapshot(ctx, prepared.project.ID, primed); err != nil {
		t.Fatal(err)
	}
	previous := generateCanonicalProposal(t, ctx, prepared)
	database := prepared.engine.Store
	if err := database.UpdateProposalOutcome(ctx, prepared.project.ID, previous.ID, outcome, proposalSuppressionReason, nil); err != nil {
		t.Fatal(err)
	}
	suppressed, err := database.ProposalByID(ctx, prepared.project.ID, previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Status != outcome || suppressed.Fingerprint != previous.Fingerprint {
		t.Fatalf("canonical proposal was not suppressed as expected: %+v", suppressed)
	}
	return previous
}

func generateCanonicalProposal(t *testing.T, ctx context.Context, prepared noveltyTestFixture) model.Direction {
	t.Helper()
	if err := prepared.engine.Ideate(WithFreshDiscovery(ctx, true), prepared.project, firstReconsiderationDay); err != nil {
		t.Fatalf("canonical first ideation failed: %v", err)
	}
	if prepared.fixture.generations == 0 {
		t.Fatalf("first ideation did not exercise generation: %d", prepared.fixture.generations)
	}
	previous := findDirectionByDayAndCategory(t, prepared.engine.Store, ctx, prepared.project.ID, firstReconsiderationDay, "expand")
	if previous.Fingerprint == "" || previous.RepositoryRevision == "" || len(previous.Evidence) == 0 {
		t.Fatalf("first run did not create a canonical, evidence-bound proposal: %+v", previous)
	}
	return previous
}

func assertCurrentReconsiderationEvidence(t *testing.T, ctx context.Context, prepared noveltyTestFixture, previous model.Direction) {
	t.Helper()
	_, _, current, unchanged, err := prepared.engine.prepareDiscovery(WithFreshDiscovery(ctx, true), prepared.project)
	if err != nil || unchanged {
		t.Fatalf("fixture did not provide fresh current discovery evidence: unchanged=%v err=%v", unchanged, err)
	}
	if current.Commit != previous.RepositoryRevision || !evidenceReferencesRemainCurrent(previous.Evidence, current.Evidence) {
		t.Fatalf("negative case changed repository authority or evidence: first revision=%q current=%q first evidence=%+v current evidence=%+v", previous.RepositoryRevision, current.Commit, previous.Evidence, current.Evidence)
	}
}

func assertNoReconsiderationAuthorization(t *testing.T, ctx context.Context, prepared noveltyTestFixture, message string) {
	t.Helper()
	if proposals, err := prepared.engine.Store.List(ctx, "proposal_reconsiderations"); err != nil || len(proposals) != 0 {
		t.Fatalf("%s: rows=%d err=%v", message, len(proposals), err)
	}
}

func modelClaimingReconsideration(t *testing.T, previous model.Direction) ([]model.Direction, []string) {
	t.Helper()
	batch := ideaBatch(t)
	distinctCategories := []string{"optimize", "harden", "simplify"}
	for i, category := range distinctCategories {
		batch[i+1].Problem = "Newly observed " + category + " issue in the repository"
		batch[i+1].Mechanism = "Use a distinct " + category + " remediation path"
		batch[i+1].Components = []string{"internal/fresh/" + category}
	}
	batch[0] = previous
	batch[0].ID = "model-claimed-reconsideration"
	batch[0].Status = "reconsider_requested"
	batch[0].PredecessorID = ""
	batch[0].RevisitReason = ""
	batch[0].Selected = false
	return batch, distinctCategories
}

func distinctUnselectedDirections(t *testing.T, ctx context.Context, prepared noveltyTestFixture, previous model.Direction) map[string]bool {
	t.Helper()
	rows, err := prepared.engine.Store.List(ctx, "directions")
	requireWorkflowSuccess(t, err)
	seen := map[string]bool{}
	for _, row := range rows {
		direction, err := store.Decode[model.Direction](row)
		requireWorkflowSuccess(t, err)
		if direction.ProjectID != prepared.project.ID {
			continue
		}
		if direction.ID == previous.ID && direction.Day == previous.Day {
			if direction.Selected {
				t.Fatalf("historical proposal became selected during ideation: %+v", direction)
			}
			continue
		}
		if direction.Day == nextReconsiderationDay {
			if direction.Selected {
				t.Fatalf("ideation auto-selected a generated direction: %+v", direction)
			}
			seen[direction.Category] = true
		}
	}
	return seen
}

func assertSuppressedProposal(t *testing.T, ctx context.Context, prepared noveltyTestFixture, previous model.Direction, outcome string) {
	t.Helper()
	suppressed, err := prepared.engine.Store.ProposalByID(ctx, prepared.project.ID, previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Status != outcome || suppressed.Fingerprint != previous.Fingerprint {
		t.Fatalf("proposal ledger lost its suppressed outcome: %+v", suppressed)
	}
	if outcome == "rejected" && suppressed.RejectionReason != proposalSuppressionReason {
		t.Fatalf("rejection history changed: %q", suppressed.RejectionReason)
	}
	if outcome == "dismissed" && suppressed.DeferralReason != proposalSuppressionReason {
		t.Fatalf("dismissal history changed: %q", suppressed.DeferralReason)
	}
}

func evidenceReferencesRemainCurrent(previous, current []model.EvidenceRef) bool {
	available := make(map[string]model.EvidenceRef, len(current))
	for _, ref := range current {
		available[ref.Kind+":"+ref.Ref] = ref
	}
	for _, ref := range previous {
		if available[ref.Kind+":"+ref.Ref] != ref {
			return false
		}
	}
	return len(previous) > 0
}

func findDirectionByDayAndCategory(t *testing.T, database *store.Store, ctx context.Context, projectID, day, category string) model.Direction {
	t.Helper()
	rows, err := database.List(ctx, "directions")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		direction, err := store.Decode[model.Direction](row)
		if err != nil {
			t.Fatal(err)
		}
		if direction.ProjectID == projectID && direction.Day == day && direction.Category == category {
			return direction
		}
	}
	t.Fatalf("missing %q direction for %s on %s", category, projectID, day)
	return model.Direction{}
}
