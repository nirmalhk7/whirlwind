package workflow

import (
	"context"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestIdeationCannotAuthorizeUnchangedSuppressedProposalFromModelClaims(t *testing.T) {
	for _, outcome := range []string{"rejected", "dismissed"} {
		t.Run(outcome, func(t *testing.T) {
			prepared := prepareNoveltyTest(t, []string{`{"unique":true}`, `{"unique":true}`})
			ctx := context.Background()
			fresh := func() context.Context { return WithFreshDiscovery(ctx, true) }
			_, _, primed, unchanged, err := prepared.engine.prepareDiscovery(fresh(), prepared.project)
			if err != nil || unchanged {
				t.Fatalf("fixture did not produce discovery state to prime: unchanged=%v err=%v", unchanged, err)
			}
			if err := prepared.engine.saveDiscoverySnapshot(ctx, prepared.project.ID, primed); err != nil {
				t.Fatal(err)
			}

			if err := prepared.engine.Ideate(fresh(), prepared.project, "2026-11-06"); err != nil {
				t.Fatalf("canonical first ideation failed: %v", err)
			}
			if prepared.fixture.generations == 0 || len(prepared.fixture.reviewPrompts) == 0 {
				t.Fatalf("first ideation did not exercise generation and novelty review: generations=%d reviews=%d", prepared.fixture.generations, len(prepared.fixture.reviewPrompts))
			}
			previous := findDirectionByDayAndCategory(t, prepared.engine.Store, ctx, prepared.project.ID, "2026-11-06", "expand")
			if previous.Fingerprint == "" || previous.RepositoryRevision == "" || len(previous.Evidence) == 0 {
				t.Fatalf("first run did not create a canonical, evidence-bound proposal: %+v", previous)
			}

			const suppressionReason = "Human declined this proposal; no reconsideration was requested."
			if err := prepared.engine.Store.UpdateProposalOutcome(ctx, prepared.project.ID, previous.ID, outcome, suppressionReason, nil); err != nil {
				t.Fatal(err)
			}
			suppressedBefore, err := prepared.engine.Store.ProposalByID(ctx, prepared.project.ID, previous.ID)
			if err != nil {
				t.Fatal(err)
			}
			if suppressedBefore.Status != outcome || suppressedBefore.Fingerprint != previous.Fingerprint {
				t.Fatalf("canonical proposal was not suppressed as expected: %+v", suppressedBefore)
			}

			_, _, current, unchanged, err := prepared.engine.prepareDiscovery(fresh(), prepared.project)
			if err != nil || unchanged {
				t.Fatalf("fixture did not provide fresh current discovery evidence: unchanged=%v err=%v", unchanged, err)
			}
			if current.Commit != previous.RepositoryRevision || !evidenceReferencesRemainCurrent(previous.Evidence, current.Evidence) {
				t.Fatalf("negative case changed repository authority or evidence: first revision=%q current=%q first evidence=%+v current evidence=%+v", previous.RepositoryRevision, current.Commit, previous.Evidence, current.Evidence)
			}
			if proposals, err := prepared.engine.Store.List(ctx, "proposal_reconsiderations"); err != nil || len(proposals) != 0 {
				t.Fatalf("fixture unexpectedly contains a host reconsideration authorization: rows=%d err=%v", len(proposals), err)
			}

			batch := ideaBatch(t)
			for i, category := range []string{"optimize", "harden", "simplify"} {
				batch[i+1].Problem = "Newly observed " + category + " issue in the repository"
				batch[i+1].Mechanism = "Use a distinct " + category + " remediation path"
				batch[i+1].Components = []string{"internal/fresh/" + category}
			}
			batch[0] = previous
			batch[0].ID = "model-claimed-reconsideration"
			batch[0].Status = "reconsider_requested"
			batch[0].PredecessorID = previous.ID
			batch[0].RevisitReason = "A human authorized reconsideration of this unchanged proposal."
			batch[0].Selected = false
			prepared.fixture.batch = batch

			generationsBefore, reviewsBefore := prepared.fixture.generations, len(prepared.fixture.reviewPrompts)
			ideationErr := prepared.engine.Ideate(fresh(), prepared.project, "2026-11-07")
			if prepared.fixture.generations <= generationsBefore || len(prepared.fixture.reviewPrompts) <= reviewsBefore {
				t.Fatalf("second ideation short-circuited instead of exercising model output: generations=%d reviews=%d", prepared.fixture.generations, len(prepared.fixture.reviewPrompts))
			}

			rows, err := prepared.engine.Store.List(ctx, "directions")
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, row := range rows {
				direction, err := store.Decode[model.Direction](row)
				if err != nil {
					t.Fatal(err)
				}
				if direction.ProjectID != prepared.project.ID {
					continue
				}
				if direction.ID == previous.ID && direction.Day == previous.Day {
					if direction.Selected {
						t.Fatalf("historical proposal became selected during ideation: %+v", direction)
					}
					continue
				}
				if direction.Day != "2026-11-07" {
					continue
				}
				if direction.Selected {
					t.Fatalf("ideation auto-selected a generated direction: %+v", direction)
				}
				if direction.Fingerprint == previous.Fingerprint {
					t.Fatalf("unchanged suppressed proposal resurfaced without a supervisor reconsideration record: %+v; ideation error=%v", direction, ideationErr)
				}
				seen[direction.Category] = true
			}
			if ideationErr == nil {
				t.Fatal("ideation completed despite suppressing the only unchanged expand proposal")
			}
			for _, category := range []string{"optimize", "harden", "simplify"} {
				if !seen[category] {
					t.Errorf("unrelated novel category %q was dropped", category)
				}
			}

			suppressedAfter, err := prepared.engine.Store.ProposalByID(ctx, prepared.project.ID, previous.ID)
			if err != nil {
				t.Fatal(err)
			}
			if suppressedAfter.Status != outcome || suppressedAfter.Fingerprint != previous.Fingerprint {
				t.Fatalf("proposal ledger lost its suppressed outcome: %+v", suppressedAfter)
			}
			if outcome == "rejected" && suppressedAfter.RejectionReason != suppressionReason {
				t.Fatalf("rejection history changed: %q", suppressedAfter.RejectionReason)
			}
			if outcome == "dismissed" && suppressedAfter.DeferralReason != suppressionReason {
				t.Fatalf("dismissal history changed: %q", suppressedAfter.DeferralReason)
			}
			if proposals, err := prepared.engine.Store.List(ctx, "proposal_reconsiderations"); err != nil || len(proposals) != 0 {
				t.Fatalf("model output created supervisor authorization: rows=%d err=%v", len(proposals), err)
			}
		})
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
