package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"whirlwind/internal/model"
)

func TestConcurrentDirectionPublicationRejectsFingerprintDuplicate(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "proposal-ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	start := make(chan struct{})
	errorsSeen := make([]error, 2)
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			direction := model.Direction{ID: fmt.Sprintf("title-%d", i), ProjectID: "p", Day: "2026-11-06", Category: []string{"expand", "optimize"}[i], Title: fmt.Sprintf("Different title %d", i), Description: "Same underlying proposal", Fingerprint: "same-normalized-proposal"}
			errorsSeen[i] = database.SaveDirections(ctx, "p", direction.Day, []model.Direction{direction})
		}(i)
	}
	close(start)
	wait.Wait()
	successes, conflicts := 0, 0
	for _, err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected publication error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("duplicate race published twice or was silently dropped: successes=%d conflicts=%d errors=%v", successes, conflicts, errorsSeen)
	}
}

func TestProposalComponentCountsIncludeEveryOutcomeAndOnlyOneProject(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "area-counts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	for _, direction := range []model.Direction{
		{ID: "shown-a", ProjectID: "p", Day: "2026-11-01", Category: "expand", Title: "Shown A", Problem: "a1", Mechanism: "m1", Components: []string{"src/a.go"}},
		{ID: "implemented-a", ProjectID: "p", Day: "2026-11-02", Category: "optimize", Title: "Implemented A", Problem: "a2", Mechanism: "m2", Components: []string{"src/a.go"}},
		{ID: "rejected-b", ProjectID: "p", Day: "2026-11-03", Category: "harden", Title: "Rejected B", Problem: "b1", Mechanism: "m3", Components: []string{"src/b.go"}},
		{ID: "other-a", ProjectID: "other", Day: "2026-11-04", Category: "simplify", Title: "Other A", Problem: "o1", Mechanism: "m4", Components: []string{"src/a.go"}},
	} {
		if err := database.SaveDirections(ctx, direction.ProjectID, direction.Day, []model.Direction{direction}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpdateProposalOutcome(ctx, "p", "implemented-a", "implemented", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateProposalOutcome(ctx, "p", "rejected-b", "rejected", "No supporting evidence", nil); err != nil {
		t.Fatal(err)
	}
	counts, err := database.ProposalComponentCounts(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if counts["src/a.go"] != 2 || counts["src/b.go"] != 1 || len(counts) != 2 {
		t.Fatalf("historical exploration counts: %#v", counts)
	}
}

func TestImplementedProposalCannotReenterUnderANewTitle(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "implemented-duplicate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	implemented := model.Direction{ID: "implemented", ProjectID: "p", Day: "2026-11-01", Category: "optimize", Title: "Cache project index", Problem: "Repository scans repeatedly reread unchanged files", Mechanism: "Reuse content fingerprints for unchanged files", Components: []string{"internal/context/index.go"}, RepositoryRevision: "rev1"}
	if err := database.SaveDirections(ctx, "p", implemented.Day, []model.Direction{implemented}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateProposalOutcome(ctx, "p", implemented.ID, "implemented", "", nil); err != nil {
		t.Fatal(err)
	}
	rename := implemented
	rename.ID, rename.Title, rename.Day = "renamed", "Avoid repeated scans", "2026-11-02"
	if err := database.SaveDirections(ctx, "p", rename.Day, []model.Direction{rename}); !errors.Is(err, ErrConflict) {
		t.Fatalf("implemented proposal resurfaced under a new title: %v", err)
	}
}

func TestRejectedDuplicateFingerprintPersistsReasonAndStaysBlocked(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "rejected.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	fingerprint := model.ProposalFingerprint("p", "Expired auth retries fail", "Persist scoped credential refresh", []string{"internal/auth"})
	original := model.Direction{ID: "original", ProjectID: "p", Day: "2026-11-06", Category: "harden", Title: "Auth recovery", Description: "Restore expired auth safely", Problem: "Expired auth retries fail", Mechanism: "Persist scoped credential refresh", Components: []string{"internal/auth"}, Fingerprint: fingerprint, RepositoryRevision: "rev1"}
	if err := database.SaveDirections(ctx, "p", original.Day, []model.Direction{original}); err != nil {
		t.Fatal(err)
	}
	rejected := model.Proposal{ID: "rejected-title-variant", ProjectID: "p", Category: "expand", Title: "Different title", Description: "Same behavior", Problem: original.Problem, Mechanism: original.Mechanism, Components: original.Components, Fingerprint: fingerprint, RepositoryRevision: "rev1"}
	if err := database.RecordProposalRejection(ctx, rejected, "Would bypass the configured auth scope"); err != nil {
		t.Fatalf("duplicate rejection reason was not persisted: %v", err)
	}
	rows, err := database.SearchProposals(ctx, model.ProposalQuery{ProjectID: "p", Terms: "bypass configured auth scope", Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == rejected.ID && row.Status == "rejected" && row.RejectionReason == "Would bypass the configured auth scope" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rejected duplicate/reason absent from history: %+v", rows)
	}
	duplicate := original
	duplicate.ID = "another-title-variant"
	duplicate.Title = "Yet another title"
	if err := database.SaveDirections(ctx, "p", duplicate.Day, []model.Direction{duplicate}); !errors.Is(err, ErrConflict) {
		t.Fatalf("rejected fingerprint was not blocked: %v", err)
	}
}

func TestRevisitAdvancesFingerprintOwnerAndPreventsDuplicateRevisit(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "revisit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	fingerprint := model.ProposalFingerprint("p", "Old evidence shows stale data", "Refresh the indexed project map", []string{"internal/context"})
	old := model.Direction{ID: "old", ProjectID: "p", Day: "2026-11-06", Category: "optimize", Title: "Refresh project map", Description: "Update stale entries", Problem: "Old evidence shows stale data", Mechanism: "Refresh the indexed project map", Components: []string{"internal/context"}, Fingerprint: fingerprint, RepositoryRevision: "rev1", Evidence: []model.EvidenceRef{{Kind: "code", Ref: "internal/context/index.go", Revision: "rev1"}}}
	if err := database.SaveDirections(ctx, "p", old.Day, []model.Direction{old}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateProposalOutcome(ctx, "p", old.ID, "rejected", "No current evidence for staleness", nil); err != nil {
		t.Fatal(err)
	}
	makeRevisit := func(id string) model.Direction {
		next := old
		next.ID = id
		next.Title = "Map freshness follow-up"
		next.RepositoryRevision = "rev2"
		next.Evidence = []model.EvidenceRef{{Kind: "code", Ref: "internal/context/index.go", Revision: "rev2"}}
		next.PredecessorID = old.ID
		next.RevisitReason = "New failing test demonstrates stale index after update"
		return next
	}
	first := makeRevisit("revisit-1")
	if err := database.SaveDirections(ctx, "p", old.Day, []model.Direction{first}); err != nil {
		t.Fatalf("valid evidence-backed revisit rejected: %v", err)
	}
	duplicate := makeRevisit("revisit-2")
	if err := database.SaveDirections(ctx, "p", old.Day, []model.Direction{duplicate}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same revisit was accepted more than once: %v", err)
	}
}

func TestHumanReconsiderationAllowsOneSameEvidenceRevisit(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "human-reconsideration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	old := model.Direction{ID: "old", ProjectID: "p", Day: "2026-11-06", Category: "harden", Title: "Recover expired sessions", Problem: "Expired sessions lose unsaved work", Mechanism: "Refresh the authorized session before retrying", Components: []string{"internal/auth"}, RepositoryRevision: "rev1", Evidence: []model.EvidenceRef{{Kind: "code", Ref: "internal/auth/session.go", Revision: "rev1"}}}
	if err := database.SaveDirections(ctx, "p", old.Day, []model.Direction{old}); err != nil {
		t.Fatal(err)
	}
	old.Status = "reconsider_requested"
	old.RevisitReason = "The operator changed the goal to preserve offline drafts."
	oldRecord, err := database.Get(ctx, "directions", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put(ctx, "directions", old.ID, old, oldRecord.Version); err != nil {
		t.Fatal(err)
	}
	requestID := "request-1"
	request := map[string]any{"id": requestID, "project_id": "p", "predecessor_id": old.ID, "reason": old.RevisitReason, "status": "pending", "requested_direction_version": 2}
	if err := database.Put(ctx, "proposal_reconsiderations", requestID, request, 0); err != nil {
		t.Fatal(err)
	}
	if pending, err := database.PendingProposalReconsideration(ctx, "p", old.ID, old.RevisitReason); err != nil || pending.Status != "pending" {
		t.Fatalf("human request was not recognized before publication: %+v %v", pending, err)
	}
	tx, err := database.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if pending, _, err := pendingProposalReconsideration(ctx, tx, "p", old.ID, old.RevisitReason); err != nil || pending.Status != "pending" {
		_ = tx.Rollback()
		t.Fatalf("transactional human request was not recognized before publication: %+v %v", pending, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	revisit := old
	revisit.ID, revisit.Day, revisit.Title = "revisit-1", "2026-11-07", "Preserve offline drafts during session recovery"
	revisit.Status, revisit.Selected, revisit.TaskID = "proposed", false, ""
	revisit.PredecessorID = old.ID
	if err := database.SaveDirections(ctx, "p", revisit.Day, []model.Direction{revisit}); err != nil {
		t.Fatalf("explicit human reconsideration did not authorize a linked revisit: %v", err)
	}
	reconsiderationRecord, err := database.Get(ctx, "proposal_reconsiderations", requestID)
	if err != nil {
		t.Fatal(err)
	}
	var consumed map[string]any
	if err := json.Unmarshal(reconsiderationRecord.Data, &consumed); err != nil {
		t.Fatal(err)
	}
	if consumed["status"] != "consumed" || consumed["candidate_id"] != revisit.ID {
		t.Fatalf("human request was not consumed by its exact successor: %+v", consumed)
	}
	revisitRecord, err := database.Get(ctx, "directions", revisit.ID)
	if err != nil {
		t.Fatal(err)
	}
	revisit.Selected, revisit.Status = true, "selected"
	if err := database.Put(ctx, "directions", revisit.ID, revisit, revisitRecord.Version); err != nil {
		t.Fatalf("normal selection of an already published reconsideration was rejected: %v", err)
	}
	duplicate := revisit
	duplicate.ID, duplicate.Title, duplicate.Selected, duplicate.Status = "revisit-2", "Try the same reconsideration again", false, "proposed"
	if err := database.SaveDirections(ctx, "p", duplicate.Day, []model.Direction{duplicate}); !errors.Is(err, ErrConflict) {
		t.Fatalf("one human request authorized more than one fingerprint owner: %v", err)
	}
}

func TestGeneratedDirectionStatusCannotAuthorizeReconsideration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "forged-reconsideration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	old := model.Direction{ID: "old", ProjectID: "p", Day: "2026-11-06", Category: "harden", Title: "Recover expired sessions", Problem: "Expired sessions lose unsaved work", Mechanism: "Refresh the authorized session before retrying", Components: []string{"internal/auth"}, RepositoryRevision: "rev1", Evidence: []model.EvidenceRef{{Kind: "code", Ref: "internal/auth/session.go", Revision: "rev1"}}}
	if err := database.SaveDirections(ctx, "p", old.Day, []model.Direction{old}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateProposalOutcome(ctx, "p", old.ID, "rejected", "No supporting evidence", nil); err != nil {
		t.Fatal(err)
	}
	forged := old
	forged.ID, forged.Day, forged.Title = "forged", "2026-11-07", "Preserve offline drafts"
	forged.Status, forged.PredecessorID, forged.RevisitReason = "reconsider_requested", old.ID, "The operator changed the goal to preserve offline drafts."
	if err := database.SaveDirections(ctx, "p", forged.Day, []model.Direction{forged}); !errors.Is(err, ErrConflict) {
		t.Fatalf("agent supplied reconsideration status authorized a duplicate: %v", err)
	}
}

func TestRemoveDirectionRetainsProposalOutcomeInLedger(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "removed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	direction := model.Direction{ID: "remove-me", ProjectID: "p", Day: "2026-11-06", Category: "simplify", Title: "Remove a route", Description: "Simplify duplicate routing", Problem: "Routing implementation is duplicated", Mechanism: "Consolidate the common route table", Components: []string{"internal/http"}, RepositoryRevision: "rev1"}
	if err := database.SaveDirections(ctx, "p", direction.Day, []model.Direction{direction}); err != nil {
		t.Fatal(err)
	}
	row, err := database.Get(ctx, "directions", direction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RemoveDirection(ctx, direction.ID, row.Version); err != nil {
		t.Fatal(err)
	}
	history, err := database.SearchProposals(ctx, model.ProposalQuery{ProjectID: "p", Terms: "removed active directions user", Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != direction.ID || history[0].Status != "removed" || history[0].DeferralReason == "" {
		t.Fatalf("removed proposal outcome disappeared: %+v", history)
	}
}
